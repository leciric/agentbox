package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"agentbox/internal/api"
	"agentbox/internal/desktop"
)

const (
	// replayLeadIn and replayLeadOut are the still moments a replayed
	// recording opens and closes on: the screen as the walkthrough found it,
	// and as it left it, long enough to read.
	replayLeadIn  = 700 * time.Millisecond
	replayLeadOut = 1500 * time.Millisecond
)

// addRecordScript makes `agentbox media record --script <file>`: a walkthrough
// rehearsed with the desktop tools' steps tool, replayed into a recording
// with no model running, so it takes as long as the walkthrough does and no
// longer. It runs in the agent, where the display is.
func addRecordScript(a *app, record *cobra.Command) {
	var script, name, target string
	var limit time.Duration
	record.Use = "record --script <file> --name <name>"
	record.Long = `Record the agent's display as a video.

With --script, replay a walkthrough into a recording: the steps the desktop
tools' steps tool takes, saved as {"steps": [...]} in a file. The pointer
moves as a hand would, each note step marks a chapter, and no model runs
while it records. A step that fails stops the recording, keeps what was
recorded, and exits non-zero naming the step. A replay that worked keeps the
script in the agent's media beside the recording, as "<name> script", so a
re-recording is the same command again.

Without --script, use start and stop around a walkthrough driven by hand.`
	record.Args = cobra.NoArgs
	record.RunE = func(cmd *cobra.Command, _ []string) error {
		if script == "" {
			return cmd.Help()
		}
		if name == "" {
			return errors.New("name the recording, like --name settings-after")
		}
		data, err := os.ReadFile(script)
		if err != nil {
			return err
		}
		s, err := desktop.ParseScript(data)
		if err != nil {
			return err
		}
		if !desktop.Running() {
			return errors.New("the display isn't running: start the browser first: agentbox browser start")
		}
		c, err := a.scopedClient(cmd, "")
		if err != nil {
			return err
		}
		return replay(cmd, c, s, script, name, target, limit)
	}
	record.Flags().StringVar(&script, "script", "", "a walkthrough to replay, as the steps tool takes it")
	record.Flags().StringVar(&name, "name", "", `with --script: a name, like "settings-after"`)
	record.Flags().StringVar(&target, "target", "auto", "with --script: auto, display or browser")
	record.Flags().DurationVar(&limit, "limit", 10*time.Minute, "with --script: stop after this long")
}

func replay(cmd *cobra.Command, c *api.Client, s desktop.Script, script, name, target string, limit time.Duration) error {
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	status, err := c.StartRecording(ctx, "", api.RecordRequest{Target: target, Input: "desktop", Name: name, LimitSeconds: int(limit.Seconds())})
	if err != nil {
		return err
	}
	start := time.Now()
	// The recording is stopped and kept however the replay ends, an
	// interrupt included.
	stop := func() (api.MediaItem, error) { return c.StopRecording(context.WithoutCancel(ctx), "") }

	time.Sleep(replayLeadIn)
	done, runErr := s.Run(ctx, desktop.RunOptions{Note: func(ctx context.Context, text string) error {
		_, err := c.AddNote(ctx, "", api.NoteRequest{Text: text})
		return err
	}})
	if runErr == nil {
		time.Sleep(replayLeadOut)
	}
	took := time.Since(start)
	item, err := stop()
	if err != nil {
		return errors.Join(runErr, fmt.Errorf("stopping the recording: %w", err))
	}
	if runErr != nil {
		_, _ = fmt.Fprintf(out, "Kept %q (%s, %d of %d steps) as %s\n", item.Name, took.Round(100*time.Millisecond), done, len(s.Steps), item.ID)
		return fmt.Errorf("%w; the recording stopped there. Fix the script and replay it, or record the rest by hand with agentbox media record start", runErr)
	}
	_, _ = fmt.Fprintf(out, "Recorded %q%s: %d steps in %s, kept as %s\n", status.Name, recordTarget(status.Target), len(s.Steps), took.Round(100*time.Millisecond), item.ID)
	if err := keepScript(ctx, c, script, name); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "The script wasn't kept beside it: %v\n", err)
	}
	return nil
}

// keepScript keeps the script that made a recording in the agent's media, in
// place of the one an earlier take of the same name kept.
func keepScript(ctx context.Context, c *api.Client, script, name string) error {
	abs, err := filepath.Abs(script)
	if err != nil {
		return err
	}
	title := name + " script"
	if items, err := c.Media(ctx, ""); err == nil {
		for _, it := range items {
			if it.Kind == "file" && it.Name == title {
				_ = c.DeleteSelfMedia(ctx, it.ID)
			}
		}
	}
	_, err = c.AddMedia(ctx, "", api.AddMediaRequest{Path: abs, Kind: "file", Name: title})
	return err
}
