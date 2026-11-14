"""Stand-in for the flights plugin's tracetree kit: given no subcommand, it
fails with the usage line the real kit prints, which is all pfm doctor's
tracetree-kit check reads."""
import sys

sys.exit("TRACETREE FAILED — usage: tracetree.py new ROOT")
