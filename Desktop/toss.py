"""Throw dragged files across the screen into conversion bins.

Flick a file fast while dragging it in Explorer or on the Desktop and a
catch layer covers the screen, with one bin per available conversion along
the edges. Let go mid-swing and the file keeps your momentum: it glides,
bounces off the screen edges, gets pulled in when it passes near a bin, and
converts if it lands. The layer accepts drops as Copy only, so Explorer never
moves or deletes the original.
"""
import ctypes
import math
import time
from collections import deque
from pathlib import Path

from PySide6.QtCore import Qt, QObject, QPointF, QRectF, QTimer, Signal
from PySide6.QtGui import QColor, QCursor, QPainter, QPen, QRadialGradient
from PySide6.QtWidgets import QApplication, QWidget
from engine import source_file
from glass_design import format_label
from windows_wheel import available_formats

ARM_SPEED = 1500.0     # px/s of cursor travel mid-drag that reveals the bins
THROW_SPEED = 900.0    # px/s at release that counts as a throw, not a drop
MAX_SPEED = 6000.0     # clamp so a mouse glitch can't teleport the file
FRICTION = 1.3         # exponential velocity decay per second
RESTITUTION = 0.6      # share of speed kept after bouncing off a screen edge
BALL = 26              # radius of the flying file
BIN_RADIUS = 64
MAGNET_RANGE = 230     # bins pull the file in from this far away
MAGNET = 9000.0        # pull at point-blank range, px/s²
FUNNEL = 4.0           # extra drag per second at point-blank range
SETTLE_SPEED = 45.0    # slower than this and far from a bin means it missed
MAX_FLIGHT = 4.0
LANDING = 0.32         # seconds spent shrinking into the bin
FADE = 0.45
ARM_TIMEOUT = 0.5      # no file drag reached the layer: get out of the way
SLOW_SPEED = 220.0
SLOW_DISARM = 0.6      # dawdling over Explorer: let ordinary drops through
SOURCE_CLASSES = ('Progman', 'WorkerW', 'CabinetWClass', 'ExploreWClass')


def speed_of(samples, now, window=0.09, stale=0.08):
    """Cursor velocity from recent (time, x, y) samples; zero if the hand stopped."""
    recent = [s for s in samples if now - s[0] <= window]
    if len(recent) < 2 or now - recent[-1][0] > stale:
        return 0.0, 0.0
    (t0, x0, y0), (t1, x1, y1) = recent[0], recent[-1]
    if t1 - t0 < 0.004:
        return 0.0, 0.0
    vx, vy = (x1-x0)/(t1-t0), (y1-y0)/(t1-t0)
    speed = math.hypot(vx, vy)
    if speed > MAX_SPEED:
        vx, vy = vx*MAX_SPEED/speed, vy*MAX_SPEED/speed
    return vx, vy


def layout_bins(count, area):
    """Spread bins evenly around the inside of area (left, top, right, bottom),
    starting at top center and walking clockwise."""
    left, top, right, bottom = area
    inset = BIN_RADIUS + 28
    left, top, right, bottom = left+inset, top+inset, right-inset, bottom-inset
    width, height = max(right-left, 1), max(bottom-top, 1)
    perimeter = 2*(width+height)
    bins = []
    for i in range(count):
        s = (width/2 + i*perimeter/count) % perimeter
        if s < width:
            bins.append((left+s, top))
        elif s < width+height:
            bins.append((right, top+s-width))
        elif s < 2*width+height:
            bins.append((right-(s-width-height), bottom))
        else:
            bins.append((left, bottom-(s-2*width-height)))
    return bins


def bin_at(bins, x, y):
    for i, (bx, by) in enumerate(bins):
        if math.hypot(bx-x, by-y) <= BIN_RADIUS:
            return i
    return -1


FLYING, MISSED = 'flying', 'missed'


class Flight:
    """The thrown file: momentum, friction, wall bounces and bin magnetism."""
    def __init__(self, x, y, vx, vy):
        self.x, self.y, self.vx, self.vy = x, y, vx, vy
        self.age = 0.0
        self.trail = deque(maxlen=12)

    def step(self, dt, area, bins):
        """Advance dt seconds. Returns FLYING, MISSED or the caught bin index."""
        # Substeps keep a fast file from tunnelling straight through a bin.
        steps = max(1, math.ceil(math.hypot(self.vx, self.vy)*dt/12))
        for _ in range(steps):
            result = self._step(dt/steps, area, bins)
            if result != FLYING:
                return result
        self.trail.append((self.x, self.y))
        return FLYING

    def _step(self, dt, area, bins):
        self.age += dt
        near = False
        for i, (bx, by) in enumerate(bins):
            dx, dy = bx-self.x, by-self.y
            distance = math.hypot(dx, dy)
            if distance <= BIN_RADIUS - BALL*.4:
                return i
            if distance < MAGNET_RANGE:
                near = True
                closeness = 1-distance/MAGNET_RANGE
                pull = MAGNET*closeness
                self.vx += dx/distance*pull*dt
                self.vy += dy/distance*pull*dt
                # A funnel, not an orbit: extra drag near the mouth lets it settle in.
                funnel = math.exp(-FUNNEL*closeness*dt)
                self.vx *= funnel; self.vy *= funnel
        decay = math.exp(-FRICTION*dt)
        self.vx *= decay; self.vy *= decay
        self.x += self.vx*dt; self.y += self.vy*dt
        left, top, right, bottom = area
        if self.x < left+BALL: self.x, self.vx = left+BALL, abs(self.vx)*RESTITUTION
        if self.x > right-BALL: self.x, self.vx = right-BALL, -abs(self.vx)*RESTITUTION
        if self.y < top+BALL: self.y, self.vy = top+BALL, abs(self.vy)*RESTITUTION
        if self.y > bottom-BALL: self.y, self.vy = bottom-BALL, -abs(self.vy)*RESTITUTION
        if self.age > MAX_FLIGHT or (not near and math.hypot(self.vx, self.vy) < SETTLE_SPEED):
            return MISSED
        return FLYING


def dropped_paths(mime):
    """Validated local files from a drop payload, or [] if any are unusable."""
    if not mime.hasUrls() or not all(u.isLocalFile() for u in mime.urls()):
        return []
    urls = mime.urls()
    if not 1 <= len(urls) <= 100:
        return []
    try:
        return [str(source_file(u.toLocalFile())) for u in urls]
    except (OSError, ValueError):
        return []


class TossLayer(QWidget):
    """Full-screen catch layer for one screen. idle → armed → aiming → flying →
    landing (converts) or missed (does nothing)."""
    convert = Signal(list, str)

    def __init__(self, ffmpeg, parent=None):
        super().__init__(parent, Qt.Tool | Qt.FramelessWindowHint | Qt.WindowStaysOnTopHint
                         | Qt.WindowDoesNotAcceptFocus)
        self.setAttribute(Qt.WA_TranslucentBackground)
        self.setAttribute(Qt.WA_ShowWithoutActivating)
        self.setAcceptDrops(True)
        self.setAccessibleName('Throw-to-convert bins')
        self.ffmpeg = ffmpeg
        self.state = 'idle'
        self.paths, self.formats, self.bins = [], [], []
        self.samples = deque(maxlen=32)   # (time, global x, global y)
        self.flight = None
        self.hover = self.target = -1
        self.changed = 0.0                # when the current state began
        self.clock = QTimer(self); self.clock.setInterval(16)
        self.clock.timeout.connect(self.tick)
        self.last_tick = None

    # Geometry: bins avoid the taskbar, the layer itself covers the whole screen.
    def area(self):
        screen = self.screen() or QApplication.primaryScreen()
        free = screen.availableGeometry().translated(-self.geometry().topLeft())
        return (free.left(), free.top(), free.right(), free.bottom())

    def enter(self, state):
        self.state = state
        self.changed = time.monotonic()
        self.update()

    def sample(self, x, y, now=None):
        self.samples.append((time.monotonic() if now is None else now, x, y))

    def arm(self, point):
        if self.state != 'idle':
            return
        screen = QApplication.screenAt(point) or QApplication.primaryScreen()
        self.setGeometry(screen.geometry())
        self.paths, self.formats, self.bins = [], [], []
        self.hover = self.target = -1
        self.enter('armed')
        self.show(); self.raise_()

    def disarm(self):
        if self.state in ('armed', 'aiming'):
            self.reset()

    def reset(self):
        self.clock.stop(); self.flight = None
        self.state = 'idle'
        self.hide()

    def dragEnterEvent(self, event):
        paths = dropped_paths(event.mimeData())
        formats = available_formats(paths, bool(self.ffmpeg()))
        if self.state not in ('armed', 'aiming') or not formats or not event.possibleActions() & Qt.CopyAction:
            event.ignore()
            # Not something we can convert: step aside so Explorer gets the drop.
            QTimer.singleShot(0, self.disarm)
            return
        self.paths, self.formats = paths, formats
        self.bins = layout_bins(len(formats), self.area())
        self.hover = bin_at(self.bins, event.position().x(), event.position().y())
        self.enter('aiming')
        event.setDropAction(Qt.CopyAction); event.accept()

    def dragMoveEvent(self, event):
        position = event.position()
        global_point = self.mapToGlobal(position.toPoint())
        self.sample(global_point.x(), global_point.y())
        self.hover = bin_at(self.bins, position.x(), position.y())
        event.setDropAction(Qt.CopyAction); event.accept()
        self.update()

    def dragLeaveEvent(self, event):
        event.accept()
        self.disarm()

    def dropEvent(self, event, now=None):
        paths = dropped_paths(event.mimeData())
        if self.state != 'aiming' or paths != self.paths or not event.possibleActions() & Qt.CopyAction:
            event.ignore(); self.disarm()
            return
        # Copy is the only acknowledged action, so the source never moves the file.
        event.setDropAction(Qt.CopyAction); event.accept()
        x, y = event.position().x(), event.position().y()
        vx, vy = speed_of(self.samples, time.monotonic() if now is None else now)
        self.flight = Flight(x, y, vx, vy)
        caught = bin_at(self.bins, x, y)
        if caught >= 0:
            self.land(caught)           # placed straight into a bin
        elif math.hypot(vx, vy) >= THROW_SPEED:
            self.enter('flying')
        else:
            self.enter('missed')        # set down gently on nothing: no-op
        self.last_tick = time.monotonic()
        self.clock.start()

    def land(self, index):
        self.target = index
        self.enter('landing')

    def tick(self):
        now = time.monotonic()
        dt = min(now - (self.last_tick or now), .05)
        self.last_tick = now
        self.advance(dt, now)

    def advance(self, dt, now=None):
        now = time.monotonic() if now is None else now
        if self.state == 'flying':
            result = self.flight.step(dt, self.area(), self.bins)
            if result == MISSED:
                self.enter('missed')
            elif result != FLYING:
                self.land(result)
        elif self.state == 'landing' and now - self.changed >= LANDING:
            paths, target = list(self.paths), self.formats[self.target]
            self.reset()
            self.convert.emit(paths, target)
            return
        elif self.state == 'missed' and now - self.changed >= FADE:
            self.reset()
            return
        self.update()

    def label(self):
        if len(self.paths) > 1:
            return str(len(self.paths))
        suffix = Path(self.paths[0]).suffix[1:].upper() if self.paths else ''
        return suffix[:4] or 'FILE'

    def paintEvent(self, event):
        p = QPainter(self); p.setRenderHint(QPainter.Antialiasing)
        if self.state in ('armed', 'aiming'):
            # Alpha 1 is invisible but still hit-testable, so drops land here.
            p.fillRect(self.rect(), QColor(0, 0, 0, 1))
        if self.state == 'armed':
            return
        progress = min((time.monotonic()-self.changed)/LANDING, 1) if self.state == 'landing' else 0
        for i, (x, y) in enumerate(self.bins):
            self.draw_bin(p, x, y, self.formats[i], i in (self.hover, self.target))
        if self.state == 'aiming':
            self.draw_caption(p, 'Throw it into a bin · let go while moving')
        if not self.flight:
            return
        fade = 1.0
        if self.state == 'missed':
            fade = max(0.0, 1-(time.monotonic()-self.changed)/FADE)
        x, y, radius = self.flight.x, self.flight.y, BALL
        if self.state == 'landing':
            bx, by = self.bins[self.target]
            x, y = x+(bx-x)*progress, y+(by-y)*progress
            radius = BALL*(1-progress*.8)
        for n, (tx, ty) in enumerate(self.flight.trail):
            alpha = int(90*fade*(n+1)/len(self.flight.trail))
            p.setPen(Qt.NoPen); p.setBrush(QColor(255, 118, 43, alpha))
            r = BALL*(.35+.5*(n+1)/len(self.flight.trail))
            p.drawEllipse(QPointF(tx, ty), r, r)
        glow = QRadialGradient(x-radius*.35, y-radius*.4, radius*1.6)
        glow.setColorAt(0, QColor(255, 196, 140, int(255*fade)))
        glow.setColorAt(1, QColor(235, 79, 12, int(245*fade)))
        p.setBrush(glow); p.setPen(QPen(QColor(255, 255, 255, int(220*fade)), 1.5))
        p.drawEllipse(QPointF(x, y), radius, radius)
        if radius > 14:
            font = p.font(); font.setPointSize(9); font.setBold(True); p.setFont(font)
            p.setPen(QColor(255, 255, 255, int(255*fade)))
            p.drawText(QRectF(x-radius, y-radius, radius*2, radius*2), Qt.AlignCenter, self.label())
        if self.state == 'missed':
            self.draw_caption(p, 'Missed. Nothing was changed.', fade)

    def draw_bin(self, p, x, y, value, active):
        p.setBrush(QColor(12, 33, 48, 40)); p.setPen(Qt.NoPen)
        p.drawEllipse(QPointF(x, y+4), BIN_RADIUS, BIN_RADIUS)
        fill = QRadialGradient(x-20, y-24, BIN_RADIUS*1.5)
        if active:
            fill.setColorAt(0, QColor(255, 179, 107, 245)); fill.setColorAt(1, QColor(235, 79, 12, 240))
        else:
            fill.setColorAt(0, QColor(255, 255, 255, 225)); fill.setColorAt(1, QColor(183, 206, 220, 200))
        p.setBrush(fill); p.setPen(QPen(QColor(255, 255, 255, 220), 1.4))
        p.drawEllipse(QPointF(x, y), BIN_RADIUS, BIN_RADIUS)
        # Dark mouth so it reads as something to throw into.
        p.setBrush(QColor(20, 28, 34, 150 if active else 90)); p.setPen(Qt.NoPen)
        p.drawEllipse(QPointF(x, y), BIN_RADIUS*.52, BIN_RADIUS*.52)
        font = p.font(); font.setPointSize(11); font.setBold(True); p.setFont(font)
        p.setPen(QColor('#ffffff'))
        p.drawText(QRectF(x-BIN_RADIUS, y-BIN_RADIUS, BIN_RADIUS*2, BIN_RADIUS*2),
                   Qt.AlignCenter | Qt.TextWordWrap, format_label(value))

    def draw_caption(self, p, text, fade=1.0):
        left, top, right, bottom = self.area()
        box = QRectF((left+right)/2-170, bottom-BIN_RADIUS*2-90, 340, 34)
        p.setBrush(QColor(239, 248, 253, int(225*fade))); p.setPen(Qt.NoPen)
        p.drawRoundedRect(box, 17, 17)
        font = p.font(); font.setPointSize(10); font.setBold(False); p.setFont(font)
        p.setPen(QColor(40, 64, 78, int(255*fade)))
        p.drawText(box, Qt.AlignCenter, text)


class TossMonitor(QObject):
    """Watch cursor speed during Explorer/Desktop drags and arm the catch layer.
    Reads button state and cursor position only; never keys or typed text."""
    def __init__(self, window, user32=None, cursor=QCursor.pos, layer=None):
        super().__init__(window)
        self.window = window
        self.cursor = cursor
        self.layer = layer or TossLayer(window.ffmpeg_path)
        self.layer.convert.connect(window.quick_convert)
        self.enabled = False
        self.press = None          # (time, QPoint) of the current button press
        self.from_explorer = False
        self.slow_since = None
        self.samples = deque(maxlen=16)
        if user32 is None:
            user32 = ctypes.WinDLL('user32', use_last_error=True)
            user32.GetAsyncKeyState.argtypes = [ctypes.c_int]
            user32.GetAsyncKeyState.restype = ctypes.c_short
            user32.GetForegroundWindow.restype = ctypes.c_void_p
            user32.GetClassNameW.argtypes = [ctypes.c_void_p, ctypes.c_wchar_p, ctypes.c_int]
        self.user = user32
        self.timer = QTimer(self); self.timer.setInterval(15)
        self.timer.timeout.connect(self.poll)

    def set_enabled(self, enabled):
        self.enabled = bool(enabled)
        self.press = None
        if self.enabled:
            self.timer.start()
        else:
            self.timer.stop()
            self.layer.disarm()

    def held(self, key):
        return bool(self.user.GetAsyncKeyState(key) & 0x8000)

    def explorer_foreground(self):
        hwnd = self.user.GetForegroundWindow() or 0
        name = ctypes.create_unicode_buffer(256)
        self.user.GetClassNameW(hwnd, name, 256)
        return name.value in SOURCE_CLASSES

    def poll(self, now=None):
        now = time.monotonic() if now is None else now
        mouse = self.held(2 if self.user.GetSystemMetrics(23) else 1)
        # Shift belongs to the wheel; Escape cancels the drag; Ctrl/Alt/Win mean
        # the user wants a specific Explorer action.
        interfering = any(self.held(k) for k in (0x10, 0x1B, 0x11, 0x12, 0x5B, 0x5C))
        busy = bool(self.window.batch and self.window.batch.isRunning())
        if not mouse or not self.enabled or interfering or busy:
            if self.press is not None:
                self.press = None; self.samples.clear()
                # The drop is delivered just after the button comes up; give it a
                # moment to land before stepping aside.
                QTimer.singleShot(0 if interfering else 250, self.layer.disarm)
            return
        point = self.cursor()
        if self.press is None:
            self.press = (now, point)
            self.from_explorer = self.explorer_foreground()
            self.slow_since = None
        if not self.from_explorer:
            return
        self.samples.append((now, point.x(), point.y()))
        self.layer.sample(point.x(), point.y(), now)
        speed = math.hypot(*speed_of(self.samples, now, window=.07))
        state = self.layer.state
        if state == 'idle':
            moved = (point - self.press[1]).manhattanLength()
            if now - self.press[0] > .12 and moved > 40 and speed > ARM_SPEED:
                self.layer.arm(point)
        elif state == 'armed' and now - self.layer.changed > ARM_TIMEOUT:
            self.layer.disarm()       # a window move or rubber band, not a file drag
        elif state == 'aiming':
            if speed < SLOW_SPEED and self.layer.hover < 0:
                self.slow_since = self.slow_since or now
                if now - self.slow_since > SLOW_DISARM:
                    self.layer.disarm()
            else:
                self.slow_since = None

    def stop(self):
        self.timer.stop()
        self.layer.reset()
        self.layer.close()
