package costsink

// armDeadPIDStatError is the native reader's refusal of pid 1<<30's start time: its /proc entry is absent.
const armDeadPIDStatError = "open /proc/1073741824/stat: no such file or directory"
