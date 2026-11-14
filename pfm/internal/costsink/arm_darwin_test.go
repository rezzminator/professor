package costsink

// armDeadPIDStatError is the native reader's refusal of pid 1<<30's start time: kern.proc.pid returns an
// empty record (EIO) and the existence probe answers ESRCH.
const armDeadPIDStatError = "read kern.proc.pid for 1073741824: process 1073741824 disappeared after input/output error: no such process: file does not exist"
