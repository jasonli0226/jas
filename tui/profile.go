package tui

import (
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// cmdProfile switches the active profile: with a name directly, bare it
// opens a picker (Ctrl+S also stores the choice as the default profile).
func (m *Model) cmdProfile(name string) tea.Cmd {
	if name != "" {
		return m.switchProfile(name)
	}
	active := m.b.Profile()
	var items []pickItem
	for _, n := range m.b.Profiles() {
		it := pickItem{label: m.clean(n), value: n}
		if n == active {
			it.desc = "active"
		}
		items = append(items, it)
	}
	m.openPicker("profile", items, m.switchProfile)
	m.picker.canDefault = true
	m.picker.onDefault = m.setDefaultProfile
	return nil
}

// setDefaultProfile stores name as the config default profile, then
// switches this session to it.
func (m *Model) setDefaultProfile(name string) tea.Cmd {
	if m.reloading {
		return m.notice(waitNotice)
	}
	if err := m.b.SetDefaultProfile(name); err != nil {
		return m.print(errStyle.Render("set default: " + m.clean(err.Error())))
	}
	return tea.Batch(m.notice("default profile "+m.clean(name)), m.switchProfile(name))
}

// switchProfile reloads the live session on profile name. Like cmdReload it
// runs on a goroutine, with trust asks through the farm ask UI; m.reloading
// holds off every other backend user until profileDoneMsg arrives.
func (m *Model) switchProfile(name string) tea.Cmd {
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
		sw, err := b.SwitchProfile(live.Session, name, ask)
		send(profileDoneMsg{sw: sw, err: err})
	}()
	return nil
}

// onProfileDone prints what the switch loaded, applies the live settings it
// changed and names the ones that wait for /new.
func (m *Model) onProfileDone(msg profileDoneMsg) tea.Cmd {
	m.reloading = false
	if msg.err != nil {
		return m.print(errStyle.Render("profile: " + m.clean(msg.err.Error())))
	}
	var cmds []tea.Cmd
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
