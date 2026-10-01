package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"agentbox/internal/api"
)

func newReportCmd(a *app) *cobra.Command {
	var message string
	var without []string
	var show, yes bool
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Send AgentBox's developers a report of a problem, with its logs",
		Long: `Send AgentBox's developers a report of a problem: what you say went wrong, with what helps
them find out why: AgentBox's version, this machine's OS and mode and how its setup stands,
the end of the daemon's log and, in VM mode, of the VM supervisor's. The app's "Report a
problem" (Settings, General) sends the same, with the app's own logs as well.

Tokens, keys, email addresses and home directories are taken out of all of it, your message
included, and the whole report is printed before anything is sent: leave a part out with
--without, or by its number when asked. It goes to agentbox.linting.dev, beside the update
check, with the same random install ID.

On a terminal, it asks for the message when -m doesn't give it, and asks before sending.
Elsewhere, -m and --yes are needed. --show prints what would be sent, and sends nothing.`,
		Example: `  agentbox report
  agentbox report -m "Creating an agent hangs at 'Starting the machine'" --without daemon-log
  agentbox report --show`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.client(cmd)
			if err != nil {
				return err
			}
			draft, err := c.ReportDraft(cmd.Context(), api.ReportDraftRequest{})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			var in *bufio.Reader
			if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
				in = bufio.NewReader(f)
			}
			sections, err := dropSections(draft.Sections, without)
			if err != nil {
				return err
			}
			printReport(out, draft, sections)
			if show {
				return nil
			}
			if in == nil && (!yes || strings.TrimSpace(message) == "") {
				return errors.New("not on a terminal: give the message with -m and add --yes to send this report")
			}
			if strings.TrimSpace(message) == "" {
				if message, err = askMessage(out, in); err != nil {
					return err
				}
			}
			if in != nil && !yes {
				if sections, err = askSections(out, in, sections); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(out, "Send this report, with %s? [y/N] ", sectionNames(sections))
				answer, _ := in.ReadString('\n')
				if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
					_, _ = fmt.Fprintln(out, "Nothing was sent.")
					return nil
				}
			}
			sent, err := c.SendReport(cmd.Context(), api.ReportRequest{Kind: api.ReportKindProblem, Message: message, Sections: sections})
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "Sent, thank you. The report's ID is %s: quote it if you write to us about it.\n", sent.ID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "what went wrong")
	cmd.Flags().StringSliceVar(&without, "without", nil, "leave these parts out, by name (system, daemon-log, vm-log)")
	cmd.Flags().BoolVar(&show, "show", false, "print what would be sent, and send nothing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "send without asking")
	return cmd
}

// dropSections is sections without the named ones, which must be among them.
func dropSections(sections []api.ReportSection, names []string) ([]api.ReportSection, error) {
	for _, n := range names {
		if !slices.ContainsFunc(sections, func(s api.ReportSection) bool { return s.ID == n }) {
			return nil, fmt.Errorf("--without %s: this report has no such part; it has %s", n, sectionNames(sections))
		}
	}
	return slices.DeleteFunc(slices.Clone(sections), func(s api.ReportSection) bool { return slices.Contains(names, s.ID) }), nil
}

// printReport prints everything the report would send, each part numbered.
func printReport(out io.Writer, draft api.ReportDraft, sections []api.ReportSection) {
	_, _ = fmt.Fprintf(out, "This report goes to %s with your message and:\n", draft.Endpoint)
	_, _ = fmt.Fprintf(out, "  install %s, version %s, %s/%s\n", draft.Install, draft.Version, draft.OS, draft.Arch)
	for i, s := range sections {
		_, _ = fmt.Fprintf(out, "\n── %d. %s: %s (%s) ──\n%s\n", i+1, s.ID, s.Title, humanBytes(int64(len(s.Content))), strings.TrimRight(s.Content, "\n"))
	}
	_, _ = fmt.Fprintln(out)
}

// askMessage reads the message from the terminal: lines until an empty one.
func askMessage(out io.Writer, in *bufio.Reader) (string, error) {
	_, _ = fmt.Fprintln(out, "What went wrong? What did you do, and what did you expect? End with an empty line.")
	var lines []string
	for {
		line, err := in.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line == "" || err != nil {
			if line != "" {
				lines = append(lines, line)
			}
			break
		}
		lines = append(lines, line)
	}
	message := strings.TrimSpace(strings.Join(lines, "\n"))
	if message == "" {
		return "", errors.New("a report needs a message: nothing was sent")
	}
	return message, nil
}

// askSections asks which parts to leave out, by their numbers.
func askSections(out io.Writer, in *bufio.Reader, sections []api.ReportSection) ([]api.ReportSection, error) {
	if len(sections) == 0 {
		return sections, nil
	}
	_, _ = fmt.Fprintf(out, "Leave any parts out? Type their numbers (like 2 3), or press Enter to keep them all: ")
	answer, _ := in.ReadString('\n')
	drop := map[int]bool{}
	for f := range strings.FieldsFuncSeq(answer, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == '\r' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(sections) {
			return nil, fmt.Errorf("%q isn't the number of a part: nothing was sent", f)
		}
		drop[n-1] = true
	}
	var kept []api.ReportSection
	for i, s := range sections {
		if !drop[i] {
			kept = append(kept, s)
		}
	}
	return kept, nil
}

func sectionNames(sections []api.ReportSection) string {
	if len(sections) == 0 {
		return "no other parts"
	}
	names := make([]string, len(sections))
	for i, s := range sections {
		names[i] = s.ID
	}
	return strings.Join(names, ", ")
}
