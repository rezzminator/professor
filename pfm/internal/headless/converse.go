package headless

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// ErrAwaitTimeout ends a wait that ran out of patience. It is not a failure of
// the chat: the message was delivered and the chat may still be working, which
// is why the partial Turn is returned with it.
var ErrAwaitTimeout = errors.New("the chat did not answer in time")

// Turn is one exchange: what the chat did after a message was delivered to it.
// Field names are a contract — a consumer scripts against them.
type Turn struct {
	Name string `json:"name"`
	// Delivered is the PROOF the message reached the model: the engine's own
	// transcript carries a new human turn. A launch prompt that was typed but
	// never submitted leaves this false — the failure this whole type exists
	// to make impossible to miss.
	Delivered bool   `json:"delivered"`
	Answer    string `json:"answer,omitempty"`
	Engine    string `json:"engine,omitempty"`
	State     string `json:"state"`
	Tools     int    `json:"tools,omitempty"`
	// Superseded says a SECOND human turn landed while this one was waiting —
	// somebody else spoke to the chat. The answer below is the newest one, but
	// it may be answering their question, not yours.
	Superseded    bool     `json:"superseded,omitempty"`
	Trace         []string `json:"trace,omitempty"`
	WaitedSeconds float64  `json:"waited_seconds"`
	// Offset is the transcript frontier this turn ended on, so a caller
	// holding a conversation open can wait for the NEXT answer from here.
	Offset int64 `json:"-"`
}

func waitForNextPoll(ctx context.Context, timerClock clock.Clock, duration time.Duration) error {
	if timerClock == nil {
		timerClock = clock.Real
	}
	timer := timerClock.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C():
		return nil
	}
}

// AwaitOptions bounds one wait.
type AwaitOptions struct {
	// Offset is the transcript frontier recorded before the message was sent.
	Offset int64
	// Timeout is the whole wait. Zero waits until the chat answers or dies.
	Timeout time.Duration
	// Poll is the sampling cadence.
	Poll time.Duration
	// Settle is how long the transcript must stay quiet after the assistant
	// speaks before the answer counts as finished. A model that says "let me
	// look" and then reaches for a tool has not answered yet, and returning
	// its preamble as the answer is the two-way equivalent of hanging up mid
	// sentence.
	Settle time.Duration
	// StopOnDelivery returns as soon as the human turn lands, without waiting
	// for the answer — the proof `run` needs that a prompt was really sent.
	StopOnDelivery bool
	// Grace is how long a chat that resolves to NOTHING is treated as still
	// arriving rather than gone. A seat spawned a moment ago is not in the
	// index yet, and calling it dead is the wrong answer to "did my prompt
	// land". Zero refuses an unknown chat immediately, which is right for a
	// chat the caller already resolved.
	Grace time.Duration
	// Progress receives one condensed line per new entry as it is written.
	Progress io.Writer
	// ResolveEvery is how often the chat is looked up again, which is how a
	// dead seat is noticed. It is deliberately slower than Poll: the lookup is
	// a fleet-wide scan, the transcript read is one file.
	ResolveEvery time.Duration
	Now          func() time.Time
	Clock        clock.Clock
}

func (options AwaitOptions) orDefaults() AwaitOptions {
	if options.Poll <= 0 {
		options.Poll = 500 * time.Millisecond
	}
	if options.Settle <= 0 {
		options.Settle = 3 * time.Second
	}
	if options.ResolveEvery <= 0 {
		options.ResolveEvery = 3 * time.Second
	}
	if options.Now == nil {
		if options.Clock == nil {
			options.Clock = clock.Real
		}
		options.Now = options.Clock.Now
	} else if options.Clock == nil {
		options.Clock = clock.Real
	}
	return options
}

// Await follows a chat's transcript from a recorded frontier until it has
// answered, and reports what it said.
//
// It reads the FILE, never the pane: an answer that scrolled is still an
// answer, and a chat that died mid-thought must be reported as dead rather
// than waited on forever. Resolve is re-run every tick for the same reason
// Watch re-runs it — a seat that disappears must be SEEN to disappear.
func Await(
	ctx context.Context,
	resolve func(context.Context) (Chat, bool, error),
	options AwaitOptions,
) (Turn, error) {
	options = options.orDefaults()
	start := options.Now()
	turn := Turn{State: StateWorking, Offset: options.Offset}
	offset := options.Offset
	path := ""
	answers := make([]string, 0, 2)
	newestRole := ""
	quietSince := start
	var chat Chat
	found := false
	resolvedAt := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return finish(turn, answers, start, options.Now()), err
		}
		// Reading the file is cheap; finding the chat is a whole fleet scan.
		// They run on different clocks so a wait can watch the transcript
		// closely without re-scanning the fleet several times a second — on a
		// loaded box that cost lands on every other chat too.
		if resolvedAt.IsZero() || options.Now().Sub(resolvedAt) >= options.ResolveEvery {
			var err error
			chat, found, err = resolve(ctx)
			if err != nil {
				return finish(turn, answers, start, options.Now()), err
			}
			resolvedAt = options.Now()
		}
		if !found {
			if options.Now().Sub(start) >= options.Grace {
				turn.State = StateMissing
				return finish(turn, answers, start, options.Now()), ErrChatGone
			}
			if options.Timeout > 0 && options.Now().Sub(start) >= options.Timeout {
				turn.State = StateMissing
				return finish(turn, answers, start, options.Now()), ErrAwaitTimeout
			}
			if err := waitForNextPoll(ctx, options.Clock, options.Poll); err != nil {
				return finish(turn, answers, start, options.Now()), err
			}
			continue
		}
		turn.Name = chat.Name
		turn.Engine = string(chat.Engine)
		if chat.Path != path {
			// The chat's record moved — a transcript that did not exist when
			// the message was sent, or a resumed thread writing a new file.
			// The frontier belonged to the old file, so the new one is read
			// whole.
			if path != "" {
				offset = 0
			}
			path = chat.Path
		}
		entries, next, err := transcript.From(ctx, path, string(chat.Engine), offset)
		if err != nil {
			return finish(turn, answers, start, options.Now()), err
		}
		offset = next
		turn.Offset = next
		for _, entry := range entries {
			if line := transcript.Condensed(entry); line != "" {
				turn.Trace = append(turn.Trace, line)
				if options.Progress != nil {
					fmt.Fprintln(options.Progress, line)
				}
			}
			switch entry.Role {
			case transcript.RoleUser:
				if turn.Delivered {
					// Someone else is talking to this chat too.
					turn.Superseded = true
				}
				turn.Delivered = true
				// A fresh human turn is a fresh question: whatever was said
				// before it answers something else.
				answers = answers[:0]
			case transcript.RoleAssistant:
				answers = append(answers, entry.Text)
			case transcript.RoleTool:
				turn.Tools++
			}
			newestRole = entry.Role
		}
		if len(entries) > 0 {
			quietSince = options.Now()
		}
		if options.StopOnDelivery && turn.Delivered {
			turn.State = StateWorking
			return finish(turn, answers, start, options.Now()), nil
		}
		answered := len(answers) > 0 &&
			assistantAnswered(newestRole) &&
			options.Now().Sub(quietSince) >= options.Settle
		if answered {
			turn.State = StateIdle
			return finish(turn, answers, start, options.Now()), nil
		}
		if !chat.Live {
			// The seat is gone. Anything it managed to say before dying is
			// still a real answer and is handed back either way; the caller
			// learns from the error that there will be no more.
			turn.State = StateDead
			return finish(turn, answers, start, options.Now()), ErrChatGone
		}
		if options.Timeout > 0 && options.Now().Sub(start) >= options.Timeout {
			turn.State = StateWorking
			return finish(turn, answers, start, options.Now()), ErrAwaitTimeout
		}
		if err := waitForNextPoll(ctx, options.Clock, options.Poll); err != nil {
			return finish(turn, answers, start, options.Now()), err
		}
	}
}

// Frontier is the transcript offset to record before speaking.
func Frontier(chat Chat) (int64, error) {
	return transcript.Size(chat.Path)
}

func finish(turn Turn, answers []string, start, now time.Time) Turn {
	turn.Answer = strings.TrimSpace(strings.Join(answers, "\n\n"))
	turn.WaitedSeconds = now.Sub(start).Seconds()
	return turn
}
