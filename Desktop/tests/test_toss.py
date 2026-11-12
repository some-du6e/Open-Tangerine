import math
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock

os.environ.setdefault('QT_QPA_PLATFORM', 'offscreen')
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from PySide6.QtCore import QMimeData, QPoint, QPointF, QUrl, Qt
from PySide6.QtGui import QDragEnterEvent, QDropEvent
from PySide6.QtWidgets import QApplication
import toss
from toss import (Flight, FLYING, MISSED, TossLayer, TossMonitor, layout_bins,
                  speed_of, bin_at, BIN_RADIUS, BALL)
from app import Window

AREA = (0, 0, 1920, 1040)


def fly(flight, bins, area=AREA, seconds=5.0):
    result = FLYING
    for _ in range(int(seconds/.016)):
        result = flight.step(.016, area, bins)
        if result != FLYING:
            break
    return result


class PhysicsTests(unittest.TestCase):
    def test_release_velocity_uses_recent_motion_only(self):
        samples = [(0.0, 0, 0), (1.0, 0, 0), (1.05, 100, 0), (1.08, 160, 0)]
        vx, vy = speed_of(samples, 1.09)
        self.assertAlmostEqual(vx, 2000, delta=1); self.assertEqual(vy, 0)
        # Hand stopped before letting go: a drop, not a throw.
        self.assertEqual(speed_of(samples, 1.3), (0.0, 0.0))
        self.assertEqual(speed_of(samples[:1], 0.0), (0.0, 0.0))

    def test_glitchy_jumps_are_clamped(self):
        vx, vy = speed_of([(0, 0, 0), (.01, 5000, 5000)], .01)
        self.assertAlmostEqual(math.hypot(vx, vy), toss.MAX_SPEED, delta=1)

    def test_bins_sit_inside_screen_and_do_not_overlap(self):
        for count in range(1, 9):
            bins = layout_bins(count, AREA)
            self.assertEqual(len(bins), count)
            for x, y in bins:
                self.assertTrue(BIN_RADIUS <= x <= AREA[2]-BIN_RADIUS)
                self.assertTrue(BIN_RADIUS <= y <= AREA[3]-BIN_RADIUS)
            for i, a in enumerate(bins):
                for b in bins[i+1:]:
                    self.assertGreater(math.dist(a, b), 2*BIN_RADIUS)
        self.assertEqual(layout_bins(1, AREA)[0][0], 960)   # top center

    def test_throw_at_a_bin_lands_in_it(self):
        bins = layout_bins(4, AREA)
        x, y = 960, 600
        for index, (bx, by) in enumerate(bins):
            d = math.dist((x, y), (bx, by))
            flight = Flight(x, y, (bx-x)/d*2500, (by-y)/d*2500)
            self.assertEqual(fly(flight, bins), index)

    def test_near_miss_gets_pulled_in(self):
        bins = [(960, 92)]
        flight = Flight(960+120, 600, 0, -2600)   # aimed 120 px to the side
        self.assertEqual(fly(flight, bins), 0)

    def test_fast_throw_does_not_tunnel_through_bin(self):
        bins = [(960, 300)]
        flight = Flight(960, 900, 0, -toss.MAX_SPEED)
        self.assertEqual(fly(flight, bins), 0)

    def test_throw_into_nothing_bounces_inside_then_misses(self):
        flight = Flight(960, 520, 3000, 0)
        result = FLYING
        for _ in range(400):
            result = flight.step(.016, AREA, [])
            self.assertTrue(BALL <= flight.x <= AREA[2]-BALL)
            if result != FLYING:
                break
        self.assertEqual(result, MISSED)
        self.assertLess(flight.vx, 0)   # it bounced back off the right edge


class LayerTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.source = Path(self.tmp.name)/'photo.png'; self.source.write_bytes(b'fixture')
        self.mime = QMimeData(); self.mime.setUrls([QUrl.fromLocalFile(str(self.source))])
        self.layer = TossLayer(lambda: '')
        self.received = []
        self.layer.convert.connect(lambda paths, target: self.received.append((paths, target)))

    def tearDown(self):
        self.layer.close(); self.tmp.cleanup()

    def aim(self):
        self.layer.arm(QPoint(100, 100))
        left, top, right, bottom = self.layer.area()
        self.center = (round((left+right)/2), round((top+bottom)/2))
        enter = QDragEnterEvent(QPoint(*self.center), Qt.CopyAction | Qt.MoveAction,
                                self.mime, Qt.LeftButton, Qt.NoModifier)
        self.layer.dragEnterEvent(enter)
        self.assertTrue(enter.isAccepted())
        self.assertEqual(enter.dropAction(), Qt.CopyAction)
        self.assertEqual(self.layer.state, 'aiming')
        self.assertEqual(len(self.layer.bins), len(self.layer.formats))

    def drop(self, point, now):
        event = QDropEvent(QPointF(*point), Qt.CopyAction | Qt.MoveAction,
                           self.mime, Qt.LeftButton, Qt.NoModifier)
        self.layer.dropEvent(event, now=now)
        return event

    def run_until_idle(self):
        for _ in range(600):
            if self.layer.state == 'idle':
                return
            self.layer.advance(.016, now=time.monotonic()+10)
        self.fail('throw never finished')

    def test_throw_flies_into_bin_and_converts_with_copy_only(self):
        self.aim()
        cx, cy = self.center
        target = len(self.layer.bins)-1
        bx, by = self.layer.bins[target]
        start = self.layer.mapToGlobal(QPoint(cx, cy)); gx, gy = start.x(), start.y()
        d = math.dist((cx, cy), (bx, by)); ux, uy = (bx-cx)/d, (by-cy)/d
        now = 50.0
        for i in range(5):   # a fast swing toward the bin ending at the release point
            self.layer.sample(gx-ux*40*(4-i), gy-uy*40*(4-i), now-.064+i*.016)
        event = self.drop((cx, cy), now)
        self.layer.clock.stop()
        self.assertTrue(event.isAccepted()); self.assertEqual(event.dropAction(), Qt.CopyAction)
        self.assertEqual(self.layer.state, 'flying')
        for _ in range(400):
            if self.layer.state != 'flying':
                break
            self.layer.advance(.016)
        self.assertEqual(self.layer.state, 'landing'); self.assertEqual(self.layer.target, target)
        self.run_until_idle()
        self.assertEqual(self.received, [([str(self.source.resolve())], self.layer.formats[target])])
        self.assertFalse(self.layer.isVisible())

    def test_gentle_drop_on_nothing_converts_nothing(self):
        self.aim()
        event = self.drop(self.center, 50.0)   # no samples: zero velocity
        self.layer.clock.stop()
        self.assertTrue(event.isAccepted())
        self.assertEqual(self.layer.state, 'missed')
        self.run_until_idle()
        self.assertEqual(self.received, [])

    def test_drop_directly_on_bin_converts(self):
        self.aim()
        self.drop(self.layer.bins[0], 50.0); self.layer.clock.stop()
        self.assertEqual(self.layer.state, 'landing')
        self.run_until_idle()
        self.assertEqual(self.received[0][1], self.layer.formats[0])

    def test_unconvertible_or_move_only_drags_are_refused(self):
        self.layer.arm(QPoint(100, 100))
        mime = QMimeData(); mime.setUrls([QUrl('https://example.com/a.png')])
        enter = QDragEnterEvent(QPoint(10, 10), Qt.CopyAction, mime, Qt.LeftButton, Qt.NoModifier)
        self.layer.dragEnterEvent(enter); self.assertFalse(enter.isAccepted())
        move = QDragEnterEvent(QPoint(10, 10), Qt.MoveAction, self.mime, Qt.LeftButton, Qt.NoModifier)
        self.layer.dragEnterEvent(move); self.assertFalse(move.isAccepted())
        self.app.processEvents()
        self.assertEqual(self.layer.state, 'idle')

    def test_drop_without_aiming_is_ignored(self):
        event = self.drop((960, 600), 50.0)
        self.assertFalse(event.isAccepted()); self.assertEqual(self.received, [])


class MonitorTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.app = QApplication.instance() or QApplication([])

    def make(self, window_class='CabinetWClass'):
        self.window = Window(global_wheel=False)
        self.keys = set()
        user = Mock()
        user.GetSystemMetrics.return_value = 0
        user.GetForegroundWindow.return_value = 7
        user.GetAsyncKeyState.side_effect = lambda key: -32768 if key in self.keys else 0
        def class_name(hwnd, buffer, size):
            buffer.value = window_class
        user.GetClassNameW.side_effect = class_name
        self.point = QPoint(500, 500)
        self.monitor = TossMonitor(self.window, user32=user, cursor=lambda: QPoint(self.point))
        self.monitor.enabled = True
        return self.monitor

    def tearDown(self):
        self.monitor.stop(); self.window.close()

    def swing(self, start=0.0):
        self.keys.add(1)
        t = start
        for _ in range(20):
            self.monitor.poll(now=t)
            t += .015; self.point += QPoint(40, 0)   # ~2700 px/s
        return t

    def test_fast_explorer_drag_arms_layer(self):
        self.make()
        self.swing()
        self.assertEqual(self.monitor.layer.state, 'armed')
        self.assertTrue(self.monitor.layer.isVisible())

    def test_slow_drag_or_other_apps_never_arm(self):
        self.make()
        self.keys.add(1)
        for i in range(40):
            self.monitor.poll(now=i*.015); self.point += QPoint(3, 0)
        self.assertEqual(self.monitor.layer.state, 'idle')
        self.monitor.stop(); self.window.close()
        self.make('Chrome_WidgetWin_1')
        self.swing()
        self.assertEqual(self.monitor.layer.state, 'idle')

    def test_shift_belongs_to_the_wheel(self):
        self.make()
        self.keys.add(0x10)
        self.swing()
        self.assertEqual(self.monitor.layer.state, 'idle')

    def test_armed_layer_steps_aside_if_no_file_drag_arrives(self):
        self.make()
        t = self.swing()
        self.monitor.layer.changed = t - 1
        self.monitor.poll(now=t)
        self.assertEqual(self.monitor.layer.state, 'idle')

    def test_landed_throw_runs_through_window_quick_convert(self):
        self.make()
        self.window.quick_convert = Mock()
        self.monitor.layer.convert.disconnect()
        self.monitor.layer.convert.connect(self.window.quick_convert)
        self.monitor.layer.convert.emit(['a.png'], 'JPEG')
        self.window.quick_convert.assert_called_once_with(['a.png'], 'JPEG')

    def test_disabled_monitor_does_not_poll(self):
        self.make()
        self.monitor.set_enabled(False)
        self.assertFalse(self.monitor.timer.isActive())
        self.monitor.set_enabled(True)
        self.assertTrue(self.monitor.timer.isActive())


if __name__ == '__main__':
    unittest.main()
