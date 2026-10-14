"""inks plugin host.

Runs Python plugins written against the `inks` / `inks_plugin` API.
Talks to the editor over stdin/stdout using one JSON object per line:

  editor -> host   {"id": n, "method": m, "params": {...}}
  host -> editor   {"id": n, "result": r} | {"id": n, "error": msg}
  host -> editor   {"rid": n, "method": m, "params": {...}}   (API calls)
  editor -> host   {"rid": n, "result": r} | {"rid": n, "error": msg}
  host -> editor   {"method": "log", "params": {"text": s}}   (notifications)
"""

import io
import json
import os
import select
import sys
import time
import traceback

_out = os.fdopen(os.dup(1), 'w', encoding='utf-8', buffering=1)
_in_fd = 0
_buffer = b''
_next_rid = 0
_timers = []  # (due, seq, fn)
_timer_seq = 0


class RemoteError(Exception):
    pass


def _send(obj):
    _out.write(json.dumps(obj, ensure_ascii=False) + '\n')
    _out.flush()


def notify(method, **params):
    _send({'method': method, 'params': params})


class _LogStream(io.TextIOBase):
    """Routes print() output from plugins to the editor console."""

    def __init__(self, kind):
        self.kind = kind

    def writable(self):
        return True

    def write(self, s):
        if s:
            notify('log', text=s, kind=self.kind)
        return len(s)


sys.stdout = _LogStream('out')
sys.stderr = _LogStream('err')


def _read_line(timeout):
    """Return one decoded message, or None if the timeout passes first."""
    global _buffer
    while b'\n' not in _buffer:
        r, _, _ = select.select([_in_fd], [], [], timeout)
        if not r:
            return None
        chunk = os.read(_in_fd, 65536)
        if not chunk:
            os._exit(0)  # editor went away
        _buffer += chunk
    line, _buffer = _buffer.split(b'\n', 1)
    if not line.strip():
        return _read_line(0)
    return json.loads(line.decode('utf-8'))


def call(method, **params):
    """Synchronous call into the editor. Handles nested editor requests."""
    global _next_rid
    _next_rid += 1
    rid = _next_rid
    _send({'rid': rid, 'method': method, 'params': params})
    while True:
        msg = _read_line(None)
        if msg.get('rid') == rid and 'method' not in msg:
            if 'error' in msg:
                raise RemoteError(msg['error'])
            return msg.get('result')
        _dispatch(msg)


def add_timer(fn, delay_ms):
    global _timer_seq
    _timer_seq += 1
    _timers.append((time.monotonic() + max(0, delay_ms) / 1000.0, _timer_seq, fn))
    _timers.sort()


def _run_timers():
    now = time.monotonic()
    while _timers and _timers[0][0] <= now:
        _, _, fn = _timers.pop(0)
        try:
            fn()
        except Exception:
            traceback.print_exc()


_handlers = {}


def handler(name):
    def wrap(fn):
        _handlers[name] = fn
        return fn
    return wrap


def _dispatch(msg):
    method = msg.get('method')
    if method is None:
        return  # stray response
    mid = msg.get('id')
    fn = _handlers.get(method)
    try:
        if fn is None:
            raise Exception('unknown method %s' % method)
        result = fn(**(msg.get('params') or {}))
        if mid is not None:
            _send({'id': mid, 'result': result})
    except Exception as e:
        tb = traceback.format_exc()
        if mid is not None:
            _send({'id': mid, 'error': '%s: %s' % (type(e).__name__, e), 'traceback': tb})
        else:
            sys.stderr.write(tb)


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    if here not in sys.path:
        sys.path.insert(0, here)
    import inks_plugin  # noqa: F401  (registers handlers)
    import compat
    compat.install_module_aliases()
    notify('ready', version=sys.version.split()[0])
    while True:
        timeout = None
        if _timers:
            timeout = max(0, _timers[0][0] - time.monotonic())
        msg = _read_line(timeout)
        if msg is not None:
            _dispatch(msg)
        _run_timers()


if __name__ == '__main__':
    sys.modules['plugin_host'] = sys.modules['__main__']
    main()
