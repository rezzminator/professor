package call

import pfmengine "github.com/rezzminator/professor/pfm/internal/engine"

// herdrRawCase is one TestHerdrCallRaw row: the native screen before and after
// the raw paste, the socket failure to inject, and the outcome wanted.
type herdrRawCase struct {
	name, body, before, after, refusal, badMethod string
	wantPayload                                   string
	styled, styleFormat                           string
	styleRefused, styleMalformed                  bool
	id                                            pfmengine.ID
	wantCode                                      int
	wantTyped                                     bool
}

// herdrRawStyleCases are TestHerdrCallRaw's styled-capture rows: a dim placeholder
// reads as an empty composer, a literal or coloured draft does not, and a refused,
// malformed or unstyled ANSI read leaves nothing typed.
func herdrRawStyleCases() []herdrRawCase {
	return []herdrRawCase{
		{
			name: "dim-placeholder", id: pfmengine.Codex, body: "a\nb",
			before: "› Ask Codex to do anything", styled: "› \x1b[2mAsk Codex to do anything\x1b[0m",
			wantTyped: true,
		},
		{
			name: "literal-placeholder-draft", id: pfmengine.Codex, body: "a\nb",
			before: "› Ask Codex to do anything", wantCode: CodeUndelivered,
		},
		{
			name: "styled-real-draft", id: pfmengine.Codex, body: "a\nb", before: "› unsent",
			styled: "› \x1b[32munsent\x1b[0m", wantCode: CodeUndelivered,
		},
		{
			name: "style-refused", id: pfmengine.Codex, body: "a\nb",
			before: "› hint", styleRefused: true, wantCode: CodeUndelivered,
		},
		{
			name: "style-malformed", id: pfmengine.Codex, body: "a\nb",
			before: "› hint", styleMalformed: true, wantCode: CodeUndelivered,
		},
		{
			name: "style-wrong-format", id: pfmengine.Codex, body: "a\nb",
			before: "› hint", styled: "› \x1b[2mhint\x1b[0m", styleFormat: "text",
			wantCode: CodeUndelivered,
		},
	}
}
