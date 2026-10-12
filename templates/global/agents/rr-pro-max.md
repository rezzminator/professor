---
name: rr-pro-max
from: rr
extends:
  - repo-lane
description: 'Maps a query exhaustively — tier 3, the ultimate: rr-pro → here. Delegate for "rr pro max", "rr-pro-max X", when every sub-area must settle whatever the cost. Returns the saved RR path, then the cited map.'
effort: medium
add:
  tools:
    - Read
pins:
  Budget: sha256:ea5a57c79f160b22bd0c863d70265d53845c945510c7d410706197cb95d3a8d3
---

## Budget

- Diggers: at most 8 a round.
- Rounds: no ceiling — digging ends only on step 5's other two conditions.
- Verification: at most 16 source pages.
