"""Coherent native process ancestry and environment snapshots."""
import errno
import observation
from observation_transition import ProcessExited


def process_tree(pids, descendants=False):
    processes = observation.process_snapshot()
    if descendants:
        family = set(pids)
        while True:
            children = {pid for pid, process in processes.items() if process["parent"] in family}
            if children <= family:
                break
            family.update(children)
        pids = sorted(family)
    result = []
    for original in dict.fromkeys(pids):
        pid, chain, seen = original, [], set()
        while pid > 0:
            if pid in seen or pid not in processes:
                raise ValueError(f"OBSERVE-PROCESS-ABSENT: pid={pid} origin={original}")
            seen.add(pid)
            process = processes[pid]
            chain.append(process)
            pid = process["parent"]
        try:
            values = observation.environment(original)
        except (FileNotFoundError, ProcessLookupError, PermissionError) as error:
            if isinstance(error, PermissionError) and error.errno != errno.EACCES:
                raise
            following = observation.process_snapshot()
            if observation.birth_presence(chain[0], following) == "present":
                raise
            raise ProcessExited(chain[0], error, following.get(original)) from error
        result.append({"pid": original, "start": chain[0]["start"], "chain": chain, "environment": values})
    return result


