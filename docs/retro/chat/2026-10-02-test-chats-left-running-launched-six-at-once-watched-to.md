# 2026-10-02 — test chats left running, launched six at once, watched to completion

Observed: comparing cache TTLs, the main chat launched six Sonnet chats at once, watched them to completion when the first few turns already answered the question, left eight earlier test chats idle, and the plugin's live proof left claude processes and ~26 sessions in the picker. The user ruled: one test chat at a time, read only its first turns, kill it the moment the answer is in, delete what it leaves.

Amend: judgment — a behaviour probe through `new` runs one chat, reads its first turns, kills it, and removes any session it spawned outside the project.
