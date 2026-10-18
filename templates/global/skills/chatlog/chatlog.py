#!/usr/bin/env python3
"""chatlog kit entry: `python3 -I chatlog.py {verb} ...`. The package chatlogkit/ beside this file does
the work; `-I` drops this file's directory from sys.path, so it is added here, resolved."""
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.realpath(__file__)))

from chatlogkit.cli import main  # noqa: E402

if __name__ == "__main__":
    main(sys.argv)
