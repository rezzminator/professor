"""The held engine-child shell used by reload and attention scenarios."""
import shlex


def background_command(hold, receipt):
    return ("printf '%s\\n' $$ > " + shlex.quote(str(receipt))
            + "; while test -e " + shlex.quote(str(hold)) + "; do sleep .02; done")
