package inject

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	defaultBodyMaxAge = 7 * 24 * time.Hour
	captionMaxRunes   = 120
)

// PreparedMessage is the exact text sent to a pane or appended to a dormant
// transcript. AutoFilePath is non-empty when the original body was persisted
// and Message is the short pointer replacing it.
type PreparedMessage struct {
	Message      string
	AutoFilePath string
	Unsigned     bool
	Warnings     []string
}

// PrepareForResume applies the same measured composer edge live delivery
// uses, but as its OWN inline-vs-file boundary: there is no live pane here
// for a paste transport to target, so — unlike live delivery — an
// over-threshold body still spills to a file pointer. Dormant transcript
// injection is still transport: an 8 KiB body belongs in a durable file, not
// as a giant synthetic user turn.
func (engine *Engine) PrepareForResume(
	ctx context.Context,
	target, engineName, body string,
) (PreparedMessage, error) {
	signed, unsigned := engine.SignForResume(ctx, body)
	if err := engine.refuseUnsigned(unsigned); err != nil {
		return PreparedMessage{}, err
	}
	return engine.prepareMessage(ctx, target, engineName, body, signed, unsigned, false, true)
}

// ErrUnsigned is returned instead of delivering a message no sender could be
// derived for.
//
// The old behaviour stamped "UNSIGNED — sender identity underivable" onto the
// message and sent it anyway. That is worse than it sounds: the recipient is
// handed an instruction from nobody, and its only defensible response is to
// refuse to act on it — so the send accomplished nothing except to look like
// it worked. The sender is the only party who can still repair the identity,
// and the sender is the one who was told nothing.
//
// Refusing here fails at the one moment somebody can fix it.
var ErrUnsigned = errors.New("refusing to send an UNSIGNED message")

// refuseUnsigned turns an underivable sender into a refusal, unless the caller
// deliberately opted in.
func (engine *Engine) refuseUnsigned(unsigned bool) error {
	if !unsigned || engine.options.AllowUnsigned {
		return nil
	}
	return fmt.Errorf(
		"%w: this process derived no identity of its own, so the recipient "+
			"would be asked to act on an instruction from nobody. If this ran "+
			"DETACHED (setsid/nohup/disowned), that is why: detaching severs "+
			"the process chain the handle is recovered from. Send from the "+
			"chat itself, state the identity (%s=$(pfm whoami) %s=<label> "+
			"<command>), or pass --allow-unsigned to send it anyway",
		ErrUnsigned, SenderSessionEnv, SenderLabelEnv,
	)
}

func (engine *Engine) prepareLiveMessage(
	ctx context.Context,
	target Target,
	body string,
	interrupted bool,
) (PreparedMessage, error) {
	signed, unsigned := engine.signedMessage(ctx, body, interrupted)
	if err := engine.refuseUnsigned(unsigned); err != nil {
		return PreparedMessage{}, err
	}
	if isHarnessCommand(body) {
		return PreparedMessage{Message: signed, Unsigned: unsigned}, nil
	}
	name := filepath.Base(target.SocketPath)
	if target.Pane != "" {
		name += "-" + target.Pane
	}
	return engine.prepareMessage(
		ctx,
		name,
		target.Engine,
		body,
		signed,
		unsigned,
		interrupted,
		false,
	)
}

// prepareMessage decides, for the ONE caller that still needs a size cap at
// all, whether signed is short enough to travel inline or must be persisted
// and replaced with a pointer. resume distinguishes the two callers:
//
//   - Live delivery (prepareLiveMessage, resume=false) never spills here —
//     an over-threshold live body routes through engine.go's transport
//     ladder into bracketed SendPaste instead, which is byte-safe at any
//     size and provable by a tail match or the composer's own paste
//     placeholder. Spilling it here would silently swap the caller's text
//     for a pointer before that transport ever got a chance to carry it —
//     the exact defect this function used to have.
//   - Dormant/resume delivery (PrepareForResume, resume=true) still spills
//     above inlineThreshold: it writes directly into a transcript file, so
//     there is no live composer for a paste transport to target, and an 8
//     KiB body has no business becoming one giant synthetic user turn.
func (engine *Engine) prepareMessage(
	ctx context.Context,
	target, engineName, body, signed string,
	unsigned, interrupted, resume bool,
) (PreparedMessage, error) {
	if !resume || utf8.RuneCountInString(signed) <= engine.inlineThreshold(engineName) {
		return PreparedMessage{Message: signed, Unsigned: unsigned}, nil
	}
	path, warnings, err := engine.persistBody(body, target)
	if err != nil {
		return PreparedMessage{}, err
	}
	pointer := bodyCaption(body) + " — read " + path + " fully"
	pointerUnsigned := false
	if resume {
		pointer, pointerUnsigned = engine.SignForResume(ctx, pointer)
	} else {
		pointer, pointerUnsigned = engine.signedMessage(ctx, pointer, interrupted)
	}
	// The pointer is signed separately from the body, so it is checked
	// separately: a body that signed and a pointer that did not must still
	// refuse rather than deliver half an identity.
	if err := engine.refuseUnsigned(pointerUnsigned); err != nil {
		return PreparedMessage{}, err
	}
	return PreparedMessage{
		Message:      pointer,
		AutoFilePath: path,
		Unsigned:     pointerUnsigned,
		Warnings:     warnings,
	}, nil
}

// inlineThreshold is the measured composer edge (TESTPLAN.md's "Measured
// composer edges" table): on the live path it is the inline-vs-paste
// boundary, on the resume path the inline-vs-file boundary — see
// prepareMessage's own comment for which caller means which.
func (engine *Engine) inlineThreshold(engineName string) int {
	// OpenCode inherits Codex's conservative bound: its composer paste edge is
	// unverified, so it gets the smaller of the two measured thresholds rather
	// than an invented one.
	if id, err := pfmengine.Parse(engineName); err == nil && id == pfmengine.Claude {
		return engine.options.ClaudeInlineMax
	}
	return engine.options.CodexInlineMax
}

func (engine *Engine) persistBody(body, target string) (string, []string, error) {
	root := engine.options.BodyRoot
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, fmt.Errorf("create inject body directory %q: %w", root, err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", nil, fmt.Errorf("secure inject body directory %q: %w", root, err)
	}
	warnings, err := engine.pruneBodies(root)
	if err != nil {
		return "", nil, err
	}
	stamp := engine.options.Clock.Now().UTC().Format("20060102T150405.000000000Z")
	stem := stamp + "-" + safeBodyTarget(target)
	for sequence := 0; sequence < 1000; sequence++ {
		name := stem + ".md"
		if sequence > 0 {
			name = fmt.Sprintf("%s-%03d.md", stem, sequence)
		}
		path := filepath.Join(root, name)
		err := writeExclusive(path, body)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", warnings, fmt.Errorf("write inject body %q: %w", path, err)
		}
		return path, warnings, nil
	}
	return "", warnings, fmt.Errorf("allocate inject body name under %q: 1000 collisions", root)
}

func (engine *Engine) pruneBodies(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("enumerate inject bodies in %q: %w", root, err)
	}
	cutoff := engine.options.Clock.Now().Add(-engine.options.BodyMaxAge)
	warnings := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("stat stale body candidate %q: %v", entry.Name(), err))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if err := os.Remove(path); err != nil {
			warnings = append(warnings, fmt.Sprintf("prune stale body %q: %v", path, err))
		}
	}
	return warnings, nil
}

func writeExclusive(path, body string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(body)
	closeErr := file.Close()
	if writeErr == nil && closeErr == nil {
		return nil
	}
	cleanupErr := os.Remove(path)
	return errors.Join(writeErr, closeErr, cleanupErr)
}

func bodyCaption(body string) string {
	line := body
	if newline := strings.IndexByte(line, '\n'); newline >= 0 {
		line = line[:newline]
	}
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		line = "Long inject body"
	}
	runes := []rune(line)
	if len(runes) > captionMaxRunes {
		line = string(runes[:captionMaxRunes-1]) + "…"
	}
	return line
}

func safeBodyTarget(target string) string {
	value := strings.Map(func(character rune) rune {
		switch {
		case unicode.IsLetter(character), unicode.IsDigit(character),
			character == '-', character == '_', character == '.':
			return character
		default:
			return '-'
		}
	}, target)
	value = strings.Trim(value, "-.")
	if value == "" {
		return "chat"
	}
	return headRunes(value, 96)
}
