package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"flock/internal/mail"
)

func newMailCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mail",
		Short: "List caught emails (send to 127.0.0.1:2525; MAIL_USERNAME picks the inbox)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := load()
			if err != nil {
				return err
			}

			messages, err := mail.List(filepath.Join(s.root, "mail"))
			if err != nil {
				return err
			}
			if len(messages) == 0 {
				cmd.Println("no mail")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tINBOX\tDATE\tTO\tSUBJECT")
			for _, m := range messages {
				fmt.Fprintf(w, "%s\t%s\t%s\t%v\t%s\n", m.ID, m.Inbox, m.Date.Local().Format("2006-01-02 15:04"), m.To, m.Subject)
			}
			return w.Flush()
		},
	}

	open := &cobra.Command{
		Use:   "open <id>",
		Short: "Open a caught email in the system mail app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			messages, err := mail.List(filepath.Join(s.root, "mail"))
			if err != nil {
				return err
			}
			for _, m := range messages {
				if m.ID == args[0] {
					return openFile(m.Path)
				}
			}
			return fmt.Errorf("no message %s", args[0])
		},
	}

	clear := &cobra.Command{
		Use:   "clear [inbox]",
		Short: "Delete caught emails (all inboxes by default)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := load()
			if err != nil {
				return err
			}
			inbox := ""
			if len(args) > 0 {
				inbox = args[0]
			}
			return mail.Clear(filepath.Join(s.root, "mail"), inbox)
		},
	}

	cmd.AddCommand(open, clear)
	return cmd
}

func openFile(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Run()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Run()
	}
	return exec.Command("xdg-open", path).Run()
}
