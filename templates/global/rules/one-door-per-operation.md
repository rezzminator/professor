# One door per operation

- Each operation (a decision, a lookup, a built command, a write) lives in one function, its door, and every caller uses that same function. Before writing one, grep for the function that already does it and call it, extending it for a missing case; never write a second copy, not for a background, scheduled or remote variant either.
- Before fixing a bug, find every caller and every copy of the operation; the fix lands in the one function, and every copy found is folded into it in the same change. Two copies drift, and a fix in one leaves the other broken.
