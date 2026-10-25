"""A private job workspace, opened before package privilege changes."""
import os
from pathlib import Path
import stat
from actions.trust import protected_read


def open_state(path, job, execution_class):
    path=Path(path)
    if path.name != "action-state-"+job or not path.is_absolute():
        raise ValueError("action_state_invalid")
    protected_read(path.parent,0,directory=True)
    parent=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW|os.O_CLOEXEC)
    try:
        created=False
        try:
            os.mkdir(path.name,0o700,dir_fd=parent)
            created=True
        except FileExistsError:
            pass
        fd=os.open(path.name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW|os.O_CLOEXEC,dir_fd=parent)
        uid=0 if execution_class=="host_admin" else 65534
        if created:
            os.fchown(fd,uid,uid)
            os.fsync(parent)
        info=os.fstat(fd)
        if not stat.S_ISDIR(info.st_mode) or info.st_uid!=uid or stat.S_IMODE(info.st_mode)!=0o700:
            os.close(fd)
            raise ValueError("action_state_invalid")
        return fd
    finally:
        os.close(parent)
