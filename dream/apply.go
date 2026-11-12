package dream

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonli0226/jas/core"
)

// KeepDreams is how many non-pending dreams pruning keeps, and so how many
// can still be undone.
const KeepDreams = 10

const (
	lockName    = ".lock"
	proposedDir = "proposed"
	beforeDir   = "before"
	newDirMode  = 0o755
)

// Suffixes for the folders an apply or undo makes beside a symlinked store's
// target: stage holds the incoming copy, old the store it replaces.
var (
	applyTags = swapTags{stage: ".dream-", old: ".before-"}
	undoTags  = swapTags{stage: ".undo-", old: ".applied-"}
)

// StaleError refuses an apply because the live store changed after the dream
// started.
type StaleError struct{ Paths []string }

func (e *StaleError) Error() string {
	return fmt.Sprintf("memory changed since the dream started (%s); re-run jas dream",
		strings.Join(e.Paths, ", "))
}

// PrunedError refuses an undo because the dream's old store is gone.
type PrunedError struct{ ID string }

func (e *PrunedError) Error() string {
	return fmt.Sprintf("dream %s was pruned; only the newest %d dreams keep their old store", e.ID, KeepDreams)
}

// FollowUpError means the apply, undo or discard took effect and is saved,
// but a later step (moving the watermark, deleting a copy) failed. The
// returned Meta is the new record.
type FollowUpError struct {
	ID  string
	Err error
}

func (e *FollowUpError) Error() string {
	return fmt.Sprintf("dream %s: done, but %v", e.ID, e.Err)
}

func (e *FollowUpError) Unwrap() error { return e.Err }

// leftoverError reports copies a finished swap could not delete. The swap
// itself took effect.
type leftoverError struct{ err error }

func (e *leftoverError) Error() string { return e.err.Error() }
func (e *leftoverError) Unwrap() error { return e.err }

// fsOps are the file operations a swap uses; tests replace them to inject
// failures.
type fsOps struct {
	rename    func(oldpath, newpath string) error
	copyTree  func(src, dst string) error
	save      func(dreamsDir string, m Meta) error
	removeAll func(path string) error
}

var osOps = fsOps{rename: os.Rename, copyTree: copyPlainTree, save: Save, removeAll: os.RemoveAll}

type swapTags struct{ stage, old string }

// lockDreams takes the profile-level lock that keeps applies, undos and
// discards from interleaving.
func lockDreams(dreamsDir string) (func() error, error) {
	return core.LockPath(filepath.Join(dreamsDir, lockName))
}

// Apply replaces the live store at memDir with the dream's proposal and
// keeps the replaced store in <id>/before/. It works from the record on disk
// (m supplies the id), which must be pending; a live store that no longer
// matches the record's base marks the dream stale and returns *StaleError.
// On any other error the live store is left as it was, unless the error
// says where the old store now is. A *FollowUpError means the apply took
// effect. Logging the event is the caller's job.
func Apply(memDir, dreamsDir string, m Meta) (Meta, error) {
	return apply(osOps, memDir, dreamsDir, m)
}

func apply(ops fsOps, memDir, dreamsDir string, m Meta) (Meta, error) {
	if !ValidID(m.ID) {
		return m, fmt.Errorf("dream: apply: invalid id %q", m.ID)
	}
	release, err := lockDreams(dreamsDir)
	if err != nil {
		return m, fmt.Errorf("dream %s: apply: %w", m.ID, err)
	}
	// Closing drops the lock; a close error cannot undo work already done.
	defer func() { _ = release() }()

	cur, err := Load(dreamsDir, m.ID)
	if err != nil {
		return m, err
	}
	if cur.State != Pending {
		return cur, fmt.Errorf("dream %s is %s, not pending", cur.ID, cur.State)
	}
	live, err := HashTree(memDir)
	if err != nil {
		return cur, err
	}
	if changed := ChangedPaths(cur.Base, live); len(changed) > 0 {
		stale := cur
		stale.State = Stale
		if err := ops.save(dreamsDir, stale); err != nil {
			return cur, errors.Join(&StaleError{Paths: changed}, err)
		}
		return stale, &StaleError{Paths: changed}
	}
	idDir := filepath.Join(dreamsDir, cur.ID)
	proposed, before := filepath.Join(idDir, proposedDir), filepath.Join(idDir, beforeDir)
	if err := checkProposal(proposed, cur); err != nil {
		return cur, err
	}

	err = swap(ops, memDir, proposed, before, cur.ID, applyTags)
	if err != nil && !isLeftover(err) {
		return cur, fmt.Errorf("dream %s: apply: %w", cur.ID, err)
	}
	// The swap took effect; a leftover is reported after saving.
	return finishApply(ops, memDir, dreamsDir, cur, proposed, before, err)
}

// finishApply records a swapped-in proposal. If the record cannot be saved
// the swap is reversed, so the store and the record never disagree.
func finishApply(ops fsOps, memDir, dreamsDir string, cur Meta, proposed, before string, leftover error) (Meta, error) {
	applied := cur
	applied.State = Applied
	if err := ops.save(dreamsDir, applied); err != nil {
		// Whatever is left of proposed/ is a partial copy of the now-live
		// proposal; clear it so the reverse swap has somewhere to put it.
		if rbErr := clearCopy(proposed); rbErr != nil {
			return cur, fmt.Errorf("dream %s: apply: record it: %v; reversing the swap failed, so the proposal is live: %w",
				cur.ID, err, rbErr)
		}
		if rbErr := swap(ops, memDir, before, proposed, cur.ID, undoTags); rbErr != nil && !isLeftover(rbErr) {
			return cur, fmt.Errorf("dream %s: apply: record it: %v; reversing the swap also failed, so the proposal is live: %w",
				cur.ID, err, rbErr)
		}
		return cur, fmt.Errorf("dream %s: apply: record it: %w (the old store is back in place)", cur.ID, err)
	}
	var follow []error
	if leftover != nil {
		follow = append(follow, leftover)
	}
	if err := SetWatermark(dreamsDir, applied.EndedAt); err != nil {
		follow = append(follow, err)
	}
	if len(follow) > 0 {
		return applied, &FollowUpError{ID: applied.ID, Err: errors.Join(follow...)}
	}
	return applied, nil
}

// checkProposal refuses a proposed/ that is missing, a link, or no longer
// matches the hashes recorded when it was written.
func checkProposal(dir string, m Meta) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("dream %s: proposal: %w", m.ID, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("dream %s: proposal %s is not a folder", m.ID, dir)
	}
	got, err := HashTree(dir)
	if err != nil {
		return err
	}
	if changed := ChangedPaths(m.Proposed, got); len(changed) > 0 {
		return fmt.Errorf("dream %s: proposal changed since it was written (%s); re-run jas dream",
			m.ID, strings.Join(changed, ", "))
	}
	return nil
}

// Undo puts back the store an applied dream replaced. It works from the
// record on disk, which must be applied, and refuses when the live store no
// longer matches the proposal. With no before/ folder it restores an absent
// store when the dream's base was empty and returns *PrunedError otherwise.
// It leaves the watermark alone. Error and *FollowUpError meanings are as
// for Apply.
func Undo(memDir, dreamsDir string, m Meta) (Meta, error) {
	return undo(osOps, memDir, dreamsDir, m)
}

func undo(ops fsOps, memDir, dreamsDir string, m Meta) (Meta, error) {
	if !ValidID(m.ID) {
		return m, fmt.Errorf("dream: undo: invalid id %q", m.ID)
	}
	idDir := filepath.Join(dreamsDir, m.ID)
	if _, err := os.Stat(filepath.Join(idDir, dreamFile)); errors.Is(err, fs.ErrNotExist) {
		return m, &PrunedError{ID: m.ID}
	}
	release, err := lockDreams(dreamsDir)
	if err != nil {
		return m, fmt.Errorf("dream %s: undo: %w", m.ID, err)
	}
	defer func() { _ = release() }() // see apply

	cur, err := Load(dreamsDir, m.ID)
	if err != nil {
		return m, err
	}
	if cur.State != Applied {
		return cur, fmt.Errorf("dream %s is %s, not applied", cur.ID, cur.State)
	}
	proposed, before := filepath.Join(idDir, proposedDir), filepath.Join(idDir, beforeDir)
	hasBefore, err := pathExists(before)
	if err != nil {
		return cur, err
	}
	if !hasBefore && len(cur.Base) > 0 {
		return cur, &PrunedError{ID: cur.ID}
	}
	if err := checkUndoable(memDir, before, hasBefore, cur); err != nil {
		return cur, err
	}
	// proposed/ only ever holds a copy of the proposal, which is live and
	// verified above, so a leftover one is safe to drop.
	if err := os.RemoveAll(proposed); err != nil {
		return cur, fmt.Errorf("dream %s: undo: clear %s: %w", cur.ID, proposed, err)
	}

	err = swap(ops, memDir, before, proposed, cur.ID, undoTags)
	if err != nil && !isLeftover(err) {
		return cur, fmt.Errorf("dream %s: undo: %w", cur.ID, err)
	}
	leftover := err
	undone := cur
	undone.State = Undone
	if err := ops.save(dreamsDir, undone); err != nil {
		// As in finishApply: before/ now only holds a partial copy of the
		// live store.
		if rbErr := clearCopy(before); rbErr != nil {
			return cur, fmt.Errorf("dream %s: undo: record it: %v; reversing the swap failed, so the old store is live: %w",
				cur.ID, err, rbErr)
		}
		if rbErr := swap(ops, memDir, proposed, before, cur.ID, applyTags); rbErr != nil && !isLeftover(rbErr) {
			return cur, fmt.Errorf("dream %s: undo: record it: %v; reversing the swap also failed, so the old store is live: %w",
				cur.ID, err, rbErr)
		}
		return cur, fmt.Errorf("dream %s: undo: record it: %w (the proposal is back in place)", cur.ID, err)
	}
	if leftover != nil {
		return undone, &FollowUpError{ID: undone.ID, Err: leftover}
	}
	return undone, nil
}

// checkUndoable refuses when the live store changed since the apply or the
// kept old store no longer matches the base it was taken as.
func checkUndoable(memDir, before string, hasBefore bool, m Meta) error {
	live, err := HashTree(memDir)
	if err != nil {
		return err
	}
	if changed := ChangedPaths(m.Proposed, live); len(changed) > 0 {
		return fmt.Errorf("memory changed since dream %s was applied (%s); undo refused",
			m.ID, strings.Join(changed, ", "))
	}
	if !hasBefore {
		return nil
	}
	kept, err := HashTree(before)
	if err != nil {
		return err
	}
	if changed := ChangedPaths(m.Base, kept); len(changed) > 0 {
		return fmt.Errorf("dream %s: the kept old store %s no longer matches (%s); undo refused",
			m.ID, before, strings.Join(changed, ", "))
	}
	return nil
}

// Discard marks a pending dream discarded, moves the watermark to its end
// and deletes its proposal, keeping the report. It never touches the live
// store. A *FollowUpError means the discard is recorded.
func Discard(dreamsDir string, m Meta) (Meta, error) {
	if !ValidID(m.ID) {
		return m, fmt.Errorf("dream: discard: invalid id %q", m.ID)
	}
	release, err := lockDreams(dreamsDir)
	if err != nil {
		return m, fmt.Errorf("dream %s: discard: %w", m.ID, err)
	}
	defer func() { _ = release() }() // see apply

	cur, err := Load(dreamsDir, m.ID)
	if err != nil {
		return m, err
	}
	if cur.State != Pending {
		return cur, fmt.Errorf("dream %s is %s, not pending", cur.ID, cur.State)
	}
	discarded := cur
	discarded.State = Discarded
	if err := Save(dreamsDir, discarded); err != nil {
		return cur, err
	}
	var follow []error
	if err := SetWatermark(dreamsDir, discarded.EndedAt); err != nil {
		follow = append(follow, err)
	}
	if err := os.RemoveAll(filepath.Join(dreamsDir, discarded.ID, proposedDir)); err != nil {
		follow = append(follow, fmt.Errorf("delete the proposal: %w", err))
	}
	if len(follow) > 0 {
		return discarded, &FollowUpError{ID: discarded.ID, Err: errors.Join(follow...)}
	}
	return discarded, nil
}

// swap puts the tree at from in place at live and moves the store it
// replaces to to, which must not exist. A missing from leaves live absent
// (the store did not exist before the apply); a missing live leaves to
// absent. A symlinked live keeps its link: the swap happens at the target.
// Each failure leaves a complete store at live (the old one, unless the
// error says otherwise and where each store is). A *leftoverError means the
// swap took effect.
func swap(ops fsOps, live, from, to, id string, tags swapTags) error {
	if ok, err := pathExists(to); err != nil || ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("%s already exists; move it away first", to)
	}
	hasFrom, err := pathExists(from)
	if err != nil {
		return err
	}
	fi, err := os.Lstat(live)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if !hasFrom {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(live), newDirMode); err != nil {
			return err
		}
		// Fails: live is still absent and from untouched.
		return ops.rename(from, live)
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := filepath.EvalSymlinks(live)
		if err != nil {
			return fmt.Errorf("memory %s is a link to a missing folder: %w", live, err)
		}
		if !hasFrom {
			return fmt.Errorf("memory %s is a link; remove the store at %s yourself", live, target)
		}
		return swapAtTarget(ops, target, from, to, id, tags)
	case !fi.IsDir():
		return fmt.Errorf("memory %s is not a folder", live)
	}
	if !hasFrom {
		// Fails: nothing moved.
		return ops.rename(live, to)
	}
	return swapDir(ops, live, from, to)
}

// swapDir swaps plain folders on one file system with two renames.
func swapDir(ops fsOps, live, from, to string) error {
	// Fails: nothing moved.
	if err := ops.rename(live, to); err != nil {
		return err
	}
	// Fails: put the old store back; only if that fails too is live missing,
	// and the error names where the old store is.
	if err := ops.rename(from, live); err != nil {
		if rbErr := ops.rename(to, live); rbErr != nil {
			return fmt.Errorf("%w; putting the old store back failed (%v): it is at %s, move it to %s",
				err, rbErr, to, live)
		}
		return fmt.Errorf("%w (the old store is back in place)", err)
	}
	return nil
}

// swapAtTarget swaps the folder a symlinked store points at, leaving the
// link alone. The incoming tree is copied beside the target first, since the
// target may sit on another file system.
func swapAtTarget(ops fsOps, target, from, to, id string, tags swapTags) error {
	stage, old := target+tags.stage+id, target+tags.old+id
	if ok, err := pathExists(old); err != nil || ok {
		if err != nil {
			return err
		}
		return fmt.Errorf("%s exists from an earlier attempt and may hold your old store; move it away first", old)
	}
	// A copy would drop links and special files, and from is deleted after
	// the swap: refuse up front, nothing changed.
	if err := checkPlainTree(from); err != nil {
		return err
	}
	// stage only ever holds a copy of a store kept elsewhere; a leftover one
	// is safe to drop.
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	// Fails (or the copy differs): target untouched.
	if err := ops.copyTree(from, stage); err != nil {
		return errors.Join(err, cleanup(stage))
	}
	if err := sameTree(from, stage); err != nil {
		return errors.Join(err, cleanup(stage))
	}
	// Fails: target untouched.
	if err := ops.rename(target, old); err != nil {
		return errors.Join(err, cleanup(stage))
	}
	// Fails: put the old store back.
	if err := ops.rename(stage, target); err != nil {
		if rbErr := ops.rename(old, target); rbErr != nil {
			return fmt.Errorf("%w; putting the old store back failed (%v): it is at %s, move it to %s",
				err, rbErr, old, target)
		}
		return errors.Join(fmt.Errorf("%w (the old store is back in place)", err), cleanup(stage))
	}
	// Fails: the new store is live and the old one complete at old; reverse
	// both renames so the record (not yet saved) and the store agree.
	if err := moveTree(ops, old, to); err != nil && !isLeftover(err) {
		return reverseTargetSwap(ops, target, stage, old, err)
	} else if err != nil {
		return errors.Join(err, leftover(ops, from))
	}
	return leftover(ops, from)
}

// reverseTargetSwap undoes a target swap whose old store could not be moved
// into the dream folder.
func reverseTargetSwap(ops fsOps, target, stage, old string, cause error) error {
	if err := ops.rename(target, stage); err != nil {
		return fmt.Errorf("%w; reversing failed (%v): the new store is live and the old store is at %s",
			cause, err, old)
	}
	if err := ops.rename(old, target); err != nil {
		return fmt.Errorf("%w; reversing failed (%v): the old store is at %s and the new one at %s; move the old one to %s",
			cause, err, old, stage, target)
	}
	return errors.Join(fmt.Errorf("%w (the old store is back in place)", cause), cleanup(stage))
}

// moveTree renames src to dst, falling back to copy, verify and remove when
// the rename fails (across file systems). On error dst does not exist and
// src is intact, unless the error is a *leftoverError: then dst is complete
// and only removing src failed.
func moveTree(ops fsOps, src, dst string) error {
	if err := ops.rename(src, dst); err == nil {
		return nil
	}
	// A copy would drop links and special files before src is deleted.
	if err := checkPlainTree(src); err != nil {
		return err
	}
	if err := ops.copyTree(src, dst); err != nil {
		return errors.Join(fmt.Errorf("move %s to %s: %w", src, dst, err), cleanup(dst))
	}
	if err := sameTree(src, dst); err != nil {
		return errors.Join(err, cleanup(dst))
	}
	return leftover(ops, src)
}

// sameTree refuses a copy whose files differ from its source's.
func sameTree(src, dst string) error {
	want, err := HashTree(src)
	if err != nil {
		return err
	}
	got, err := HashTree(dst)
	if err != nil {
		return err
	}
	if changed := ChangedPaths(want, got); len(changed) > 0 {
		return fmt.Errorf("copy %s to %s differs (%s)", src, dst, strings.Join(changed, ", "))
	}
	return nil
}

// leftover deletes a copy no longer needed; failing to is not fatal.
func leftover(ops fsOps, path string) error {
	if err := ops.removeAll(path); err != nil {
		return &leftoverError{err: fmt.Errorf("delete leftover %s: %w", path, err)}
	}
	return nil
}

// clearCopy deletes a leftover copy of the live store after a swap.
func clearCopy(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete leftover copy %s: %w", path, err)
	}
	return nil
}

// cleanup deletes a partial copy after a failed step, for errors.Join.
func cleanup(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("delete partial copy %s: %w", path, err)
	}
	return nil
}

func isLeftover(err error) bool {
	var lo *leftoverError
	return errors.As(err, &lo)
}

func pathExists(p string) (bool, error) {
	_, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
