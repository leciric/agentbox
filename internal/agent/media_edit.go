package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/desktop"
	"agentbox/internal/state"
)

// A recording is edited as it is stopped (desktop.Edit): the camera zooms in
// on the clicks, and the notes made while it recorded are its captions. It is
// made in the agent, with its own CPU, from what the recorder left beside the
// video, and with no model: the clicks and keys it saw, and the stretches it
// cut. --no-edit leaves it out.

// recordingNotes are the agent's notes made since a recording started: its
// chapters, which the review lists, and which the edit puts on it.
func (m *Manager) recordingNotes(ctx context.Context, a state.Agent, since time.Time) []desktop.Note {
	items, err := m.Store.Media(ctx, a.Project, a.Name)
	if err != nil {
		return nil
	}
	var notes []desktop.Note
	for _, it := range items {
		if it.Kind != MediaNote || it.CreatedAt.Before(since) {
			continue
		}
		notes = append(notes, desktop.Note{T: float64(it.CreatedAt.UnixMicro()) / 1e6, Text: noteCaption(it)})
	}
	slices.SortFunc(notes, func(a, b desktop.Note) int { return cmp.Compare(a.T, b.T) })
	return notes
}

// noteCaption is what a note's caption says: its name, which the review
// titles its chapter with, unless that is its first line cut short.
func noteCaption(n state.Media) string {
	if first, _, _ := strings.Cut(n.Text, "\n"); strings.HasSuffix(n.Name, "…") && first != "" {
		return strings.TrimSpace(first)
	}
	return n.Name
}

// recordingTitle is the title card a recording opens on: what the agent is
// doing, and the recording's name under it, or the name alone.
func (m *Manager) recordingTitle(a state.Agent, name string) [2]string {
	title := readable(name)
	if t := strings.TrimSpace(a.Title); t != "" {
		return [2]string{t, title}
	}
	return [2]string{title, ""}
}

// readable turns a name like "settings-after" into words.
func readable(name string) string {
	words := strings.Join(strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' }), " ")
	if words == "" {
		return name
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// editRecordingScript edits the stopped recording in the agent, into
// recording.edited.mp4 beside it. It runs niced: the agent may be working.
func editRecordingScript(st recordingState, notes []desktop.Note, title [2]string) string {
	var lines strings.Builder
	for _, n := range notes {
		b, _ := json.Marshal(n)
		lines.Write(b)
		lines.WriteByte('\n')
	}
	out := `"$dir/recording.mp4"`
	if st.Staged {
		out = `"$dir/out/recording.mp4"`
	}
	return fmt.Sprintf(`set -eu
dir="$HOME/%[1]s"
out=%[2]s
edited="${out%%.mp4}.edited.mp4"
rm -f "$edited"
printf '%%s' %[3]s >"$dir/recording.notes"
nice -n 10 agentbox desktop edit --events "$dir/recording.events" --cuts "$dir/recording.cuts" --notes "$dir/recording.notes" --start %[4]s --title %[5]s --subtitle %[6]s "$out" "$edited" 2>>"$dir/recording.log" || {
  rm -f "$edited"; echo "the edit failed:" >&2; tail -n 3 "$dir/recording.log" >&2; exit 1
}`, agentStateDir, out, shellQuote(lines.String()), strconv.FormatFloat(float64(st.StartedAt.UnixMicro())/1e6, 'f', 6, 64), shellQuote(title[0]), shellQuote(title[1]))
}
