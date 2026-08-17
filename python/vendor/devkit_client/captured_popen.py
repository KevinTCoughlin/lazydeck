import logging
import subprocess
import threading

logger = logging.getLogger(__name__)

# Even on Linux the stderr/stdout of child processes is often silenced or not accessible,
# this enables a capture of the combined stderr/stdout output to the status window, via the logging facilities
# similar to Popen.communicate, but with threads
class CapturedPopenFactory:
    def __init__(self):
        self._enabled = True
        self.fds = []

    @property
    def enabled(self):
        return self._enabled

    @enabled.setter
    def enabled(self, v):
        self._enabled = v

    def on_shutdown_signal(self, **kwargs):
        # Closing when we exit since we won't be polling anymore, to avoid blocking if the pipes fill up
        if len(self.fds) == 0:
            return
        logger.info(f'CapturedPopenFactory closing {len(self.fds)} child process output streams.')
        for f in self.fds:
            f.close()

    def set_shutdown_signal(self, s):
        s.connect(self.on_shutdown_signal)

    def _thread_read(self, f):
        for l in f.readlines():
            logger.info(l.strip('\n'))
        self.fds.remove(f)

    def Popen(self, cmd, cwd=None, creationflags=0):
        if not self.enabled:
            return subprocess.Popen(
                cmd,
                cwd=cwd,
                creationflags=creationflags,
            )

        p = subprocess.Popen(
            cmd,
            cwd=cwd,
            creationflags=creationflags,
            text=True,
            bufsize=1,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
        )
        self.fds.append(p.stdout)
        threading.Thread(target=self._thread_read, args=(p.stdout, ), daemon=True).start()
        return p
