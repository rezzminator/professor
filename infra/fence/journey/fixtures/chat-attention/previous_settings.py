"""Launch the dedicated pre-upgrade-input role while preserving the active account."""
from contextlib import contextmanager
import hashlib
import os
from pathlib import Path
import stat
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from chat import ensure_role, select
from faults import preserved_file
from runtime import read_json, write_json, write_text


@contextmanager
def previous_settings(root, account="1"):
    root = Path(root)
    provenance = read_json(root / "previous-install/provenance.json")
    recorded = provenance["accounts"][account]
    artifact = root / "previous-install" / account / "settings.json"
    raw = artifact.read_bytes()
    digest = hashlib.sha256(raw).hexdigest()
    if str(artifact) != recorded["artifact"] or digest != recorded["sha256"]:
        raise ValueError(f"ATTENTION-PREVIOUS-INPUT: artifact differs from actual prior install: {artifact}")
    if stat.S_IMODE(artifact.stat().st_mode) != 0o400 or not provenance["pfm_version"]:
        raise ValueError(f"ATTENTION-PREVIOUS-PROVENANCE: read-only artifact/version required: {artifact}")
    source = Path(recorded["source"])
    with preserved_file(source):
        write_text(source, raw.decode(), mode=recorded["mode"])
        actual = hashlib.sha256(source.read_bytes()).hexdigest()
        if actual != digest:
            raise ValueError(f"ATTENTION-PREVIOUS-LAUNCH-INPUT: expected={digest} actual={actual} artifact={source}")
        yield {"account": account, "source": str(source), "artifact": str(artifact),
               "sha256": actual, "pfm_version": provenance["pfm_version"],
               "pfm_sha256": provenance["pfm_sha256"]}


def prepare(branch, fixture, output):
    output = Path(output)
    receipt = Path(str(output) + ".launch-input.json")
    role = "forecast-previous-settings"
    current = select(branch, role)
    if current:
        evidence = read_json(receipt)
        if evidence["session_id"] != current["session_id"]:
            raise ValueError(f"ATTENTION-PREVIOUS-IDENTITY: {receipt}")
    else:
        with previous_settings(os.environ["JOURNEY_ROOT"]) as evidence:
            current = ensure_role(branch, role, "journey-previous-" + os.environ["PFM_RUN"], fixture)
            write_json(receipt, {**evidence, "session_id": current["session_id"]})
    write_json(output, current)
