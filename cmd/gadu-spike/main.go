// gadu-spike is a manual test program for the libgadu layer (SPEC.md phase 0).
//
// It logs in, prints the contact list and all events, and can send one message.
// The password comes from the GG_PASSWORD environment variable or from a prompt.
//
//	GG_UIN=123456 gadu-spike -to 654321 -msg "test" -wait 2m
package main

import (
	"context"
	"flag"
	"fmt"
	"html"
	"os"
	"os/signal"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/term"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
	"github.com/asmbk/matrix-gadu/pkg/gadu/libgadu"
)

func main() {
	to := flag.String("to", "", "GG number to send a test message to")
	msg := flag.String("msg", "matrix-gadu test", "text of the test message")
	wait := flag.Duration("wait", time.Minute, "how long to print events after login")
	multilogon := flag.Bool("multilogon", true, "request multilogon")
	tlsEnabled := flag.Bool("tls-enabled", false, "accept a bad server certificate")
	verbose := flag.Bool("v", false, "print debug logs of the GG layer")
	flag.StringVar(&dumpContacts, "dump-contacts", "", "write the raw contact list to this file")
	flag.Parse()

	if err := run(*to, *msg, *wait, *multilogon, *tlsEnabled, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(to, text string, wait time.Duration, multilogon, tlsEnabled, verbose bool) error {
	uin, err := gadu.ParseUIN(os.Getenv("GG_UIN"))
	if err != nil {
		return fmt.Errorf("set GG_UIN: %w", err)
	}
	password := os.Getenv("GG_PASSWORD")
	if password == "" {
		fmt.Fprint(os.Stderr, "GG password: ")
		pw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		password = string(pw)
	}

	level := zerolog.InfoLevel
	if verbose {
		level = zerolog.TraceLevel
	}
	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).Level(level).With().Timestamp().Logger()
	log.Info().Str("libgadu", libgadu.Version()).Stringer("uin", uin).Bool("multilogon", multilogon).Msg("Connecting")

	cfg := gadu.Config{UIN: uin, Password: password, Status: gadu.StatusAvailable, Multilogon: multilogon, Log: log}
	if tlsEnabled {
		cfg.TLS = gadu.TLSEnabled
	}
	sess := libgadu.New(cfg)
	defer sess.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	loginCtx, cancel := context.WithTimeout(ctx, time.Minute)
	err = sess.Connect(loginCtx)
	cancel()
	if err != nil {
		return err
	}
	log.Info().Msg("Logged in")

	if err := sess.RequestContactList(ctx); err != nil {
		log.Warn().Err(err).Msg("Contact list request failed")
	}
	if to != "" {
		peer, err := gadu.ParseUIN(to)
		if err != nil {
			return err
		}
		seq, err := sess.SendMessage(ctx, peer, `<span style="color:#000000; font-family:'MS Shell Dlg 2'; font-size:9pt; ">`+html.EscapeString(text)+`</span>`)
		if err != nil {
			return fmt.Errorf("send message: %w", err)
		}
		log.Info().Uint32("seq", uint32(seq)).Stringer("to", peer).Msg("Sent test message")
	}

	timeout := time.After(wait)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timeout:
			log.Info().Msg("Done")
			return nil
		case evt, ok := <-sess.Events():
			if !ok {
				log.Warn().Msg("Session ended")
				return nil
			}
			printEvent(log, evt)
		}
	}
}

// dumpContacts is the file for the raw contact list, or "" to skip it.
var dumpContacts string

func printEvent(log zerolog.Logger, evt gadu.Event) {
	switch e := evt.(type) {
	case *gadu.MessageEvent:
		log.Info().Stringer("from", e.Sender).Time("time", e.Time).Bool("offline", e.Offline).Str("plain", e.Plain).Str("html", e.HTML).Msg("Message")
	case *gadu.OwnMessageEvent:
		log.Info().Stringer("to", e.Recipient).Time("time", e.Time).Str("plain", e.Plain).Str("html", e.HTML).Msg("Own message from another session (multilogon works)")
	case *gadu.ContactListEvent:
		log.Info().Int("count", len(e.Contacts)).Uint32("version", e.Version).Msg("Contact list")
		if dumpContacts != "" {
			if err := os.WriteFile(dumpContacts, []byte(e.Raw), 0o600); err != nil {
				log.Error().Err(err).Msg("Failed to write the contact list")
			}
		}
		for _, c := range e.Contacts {
			log.Info().Stringer("uin", c.UIN).Str("show_name", c.ShowName).Str("nickname", c.Nickname).Str("full_name", c.FullName()).Msg("  contact")
		}
	case *gadu.MultilogonInfoEvent:
		log.Info().Int("sessions", len(e.Sessions)).Msg("Multilogon info (multilogon works)")
		for _, s := range e.Sessions {
			log.Info().Uint64("id", s.ID).Str("name", s.Name).Time("since", s.LogonTime).Msg("  session")
		}
	default:
		log.Info().Str("type", fmt.Sprintf("%T", evt)).Any("event", evt).Msg("Event")
	}
}
