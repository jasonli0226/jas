package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// cmdProfile switches the active profile: with a name directly, bare it
// opens a picker (Ctrl+S also stores the choice as the default profile).
func (m *Model) cmdProfile(name string) tea.Cmd {
	if name != "" {
		return m.switchProfile(name, false)
	}
	active := m.profile
	var items []pickItem
	for _, n := range m.b.Profiles() {
		it := pickItem{label: m.clean(n), value: n}
		if n == active {
			it.desc = "active"
		}
		items = append(items, it)
	}
	m.openPicker("profile", items, func(n string) tea.Cmd { return m.switchProfile(n, false) })
	m.picker.canDefault = true
	m.picker.onDefault = m.setDefaultProfile
	return nil
}

// setDefaultProfile stores name as the config default profile, then
// switches this session to it; both run on the switch goroutine.
func (m *Model) setDefaultProfile(name string) tea.Cmd { return m.switchProfile(name, true) }

// switchProfile reloads the live session on profile name. Like cmdReload it
// runs on a goroutine, with trust asks through the farm ask UI; m.reloading
// holds off every other backend user until profileDoneMsg arrives.
func (m *Model) switchProfile(name string, asDefault bool) tea.Cmd {
	if m.reloading || m.running() {
		return m.notice(waitNotice)
	}
	if m.live == nil {
		return nil
	}
	m.reloading = true
	send, live, b := m.send, m.live, m.b
	go func() {
		ask := func(prompt string) string {
			ch := make(chan string, 1)
			send(farmAskMsg{prompt: prompt, reply: ch})
			ans, ok := <-ch
			if !ok {
				return ""
			}
			return ans
		}
		if asDefault {
			if err := b.SetDefaultProfile(name); err != nil {
				send(profileDoneMsg{err: fmt.Errorf("set default: %w", err)})
				return
			}
		}
		sw, err := b.SwitchProfile(live.Session, name, ask)
		send(profileDoneMsg{sw: sw, err: err, defaulted: asDefault})
	}()
	return nil
}

// onProfileDone prints what the switch loaded, applies the live settings it
// changed and names the ones that wait for /new.
func (m *Model) onProfileDone(msg profileDoneMsg) tea.Cmd {
	m.reloading = false
	m.profile = m.b.Profile() // no switch is in flight now: safe to read
	m.tr.Skills = m.b.SkillFiles()
	if msg.err != nil {
		text := "profile: " + m.clean(msg.err.Error())
		if msg.defaulted {
			text += " (the default profile was stored)"
		}
		return m.print(errStyle.Render(text))
	}
	// The banner's [Profile] row is already in the scrollback and can't be
	// rewritten: print a fresh one so the transcript shows the switch.
	cmds := []tea.Cmd{m.print(dimStyle.Render("[Profile]") + " " + m.clean(m.profile))}
	if msg.defaulted {
		cmds = append(cmds, m.notice("default profile "+m.clean(m.profile)))
	}
	if len(msg.sw.Notices) > 0 {
		cmds = append(cmds, m.notice(msg.sw.Notices...))
	}
	for _, k := range slices.Sorted(maps.Keys(msg.sw.Live)) {
		cmds = append(cmds, m.applyLive(k, msg.sw.Live[k]))
	}
	if len(msg.sw.Deferred) > 0 {
		cmds = append(cmds, m.notice(strings.Join(msg.sw.Deferred, ", ")+" take effect on /new"))
	}
	return tea.Batch(cmds...)
}
