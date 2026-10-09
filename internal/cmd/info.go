package cmd

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/munepi/ctango/internal/ctanapi"
)

func newFieldsCmd(f *Factory) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "fields",
		Short: "Show the fields accepted by the CTAN submission API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fields, raw, err := f.SubmitClient().Fields(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeRawJSON(f.IO.Out, raw)
			}
			names := make([]string, 0, len(fields))
			for n := range fields {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				info := fields[n]
				var attrs []string
				if v, ok := info["maxsize"]; ok {
					attrs = append(attrs, fmt.Sprintf("max %v", v))
				}
				if info["nullable"] == false || info["blank"] == false {
					attrs = append(attrs, "required")
				}
				for _, k := range []string{"list", "url", "email", "file"} {
					if info[k] == true {
						attrs = append(attrs, k)
					}
				}
				fmt.Fprintf(f.IO.Out, "%-13s %-40v %s\n", n, info["text"], strings.Join(attrs, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print the raw JSON")
	return cmd
}

func newPkgCmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pkg <command>",
		Short: "Look up packages on CTAN",
	}
	var jsonOut bool
	view := &cobra.Command{
		Use:   "view <pkg>",
		Short: "Show what CTAN knows about a package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			api := f.APIClient()
			p, raw, err := api.Package(ctx, args[0])
			if errors.Is(err, ctanapi.ErrNotFound) {
				return fmt.Errorf("%s: %w", args[0], err)
			}
			if err != nil {
				return err
			}
			if jsonOut {
				return writeRawJSON(f.IO.Out, raw)
			}
			var authors []string
			for _, a := range p.Authors {
				if au, err := api.Author(ctx, a.ID); err == nil {
					authors = append(authors, au.Name())
				} else {
					authors = append(authors, a.ID)
				}
			}
			w := f.IO.Out
			fmt.Fprintf(w, "%s -- %s\n\n", f.IO.Bold(p.ID), p.Caption)
			row := func(k string, v ...string) {
				if s := strings.Join(nonEmpty(v), ", "); s != "" {
					fmt.Fprintf(w, "  %-11s %s\n", k+":", s)
				}
			}
			row("Version", strings.TrimSpace(p.Version.Number+" "+p.Version.Date))
			row("Authors", authors...)
			row("License", p.License...)
			row("Topics", p.Topics...)
			row("CTAN path", p.CTANPath())
			row("Home", p.Home...)
			row("Bugs", p.Bugs...)
			row("Repository", p.Repository...)
			row("TeX Live", p.TeXLive)
			row("MiKTeX", p.MiKTeX)
			row("URL", "https://ctan.org/pkg/"+p.ID)
			return nil
		},
	}
	view.Flags().BoolVar(&jsonOut, "json", false, "Print the raw JSON")
	cmd.AddCommand(view)
	return cmd
}

func nonEmpty(v []string) []string {
	var out []string
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
