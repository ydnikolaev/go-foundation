#!/usr/bin/env python3
# managed by devkit — do not edit; fix upstream in ~/Developer/projects/devkit
"""proc-cwd: the working directory of other processes, read in this one process and never through `lsof`.

    python3 scripts/kit/proc-cwd.py <pid>...

Prints `<pid> <cwd>` for each readable pid, one line each. A pid that is gone, unreadable or not a number prints
nothing. Always exit 0. Linux reads /proc/<pid>/cwd; macOS reads libproc's proc_pidinfo(PROC_PIDVNODEPATHINFO)
through ctypes. Stdlib only, python3 >= 3.9.
"""
import ctypes
import ctypes.util
import os
import sys

PROC_PIDVNODEPATHINFO = 9
VNODE_INFO_SIZE = 152  # struct vnode_info (sys/proc_info.h); the NUL-terminated vip_path[MAXPATHLEN] follows it
MAXPATHLEN = 1024
VNODE_INFO_PATH_SIZE = VNODE_INFO_SIZE + MAXPATHLEN
PATH_INFO_SIZE = 2 * VNODE_INFO_PATH_SIZE  # struct proc_vnodepathinfo: pvi_cdir, then pvi_rdir


def _libproc():
    try:
        lib = ctypes.CDLL(ctypes.util.find_library("proc") or "/usr/lib/libproc.dylib", use_errno=True)
        lib.proc_pidinfo.argtypes = [ctypes.c_int, ctypes.c_int, ctypes.c_uint64, ctypes.c_void_p, ctypes.c_int]
        lib.proc_pidinfo.restype = ctypes.c_int
        return lib
    except (OSError, AttributeError):
        return None


def _cwd_darwin(lib, pid):
    buf = ctypes.create_string_buffer(PATH_INFO_SIZE)
    if lib.proc_pidinfo(pid, PROC_PIDVNODEPATHINFO, 0, buf, PATH_INFO_SIZE) != PATH_INFO_SIZE:
        return None
    path = buf.raw[VNODE_INFO_SIZE:VNODE_INFO_PATH_SIZE].split(b"\0", 1)[0]
    return os.fsdecode(path) or None


def _cwd_proc(pid):
    try:
        return os.readlink("/proc/%d/cwd" % pid) or None
    except OSError:
        return None


def main(argv):
    darwin = sys.platform == "darwin"
    lib = _libproc() if darwin else None
    for arg in argv:
        if not arg.isdigit() or not arg.isascii():
            continue
        pid = int(arg)
        if pid <= 0 or pid > 2**31 - 1:
            continue
        cwd = (_cwd_darwin(lib, pid) if lib is not None else None) if darwin else _cwd_proc(pid)
        if cwd and "\n" not in cwd:
            print("%d %s" % (pid, cwd))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
