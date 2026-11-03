"""Make the journey modules importable when a fixture runs as a script."""
from pathlib import Path
import sys

JOURNEY = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(JOURNEY))
