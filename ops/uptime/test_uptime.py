"""Логика монитора без сети: подтверждение падения и «без спама». python -m unittest ops/uptime/test_uptime.py"""
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(__file__))
import uptime  # noqa: E402


def seq(*results):
    it = iter(results)
    return lambda url: next(it)


class Check(unittest.TestCase):
    def test_одна_неудача_не_падение(self):
        up, _ = uptime.check("u", probe_fn=seq((False, "HTTP 502"), (True, "HTTP 200")), sleep=lambda s: None)
        self.assertTrue(up)

    def test_все_попытки_неудачны_падение(self):
        sleeps = []
        up, detail = uptime.check("u", attempts=3, pause=20, probe_fn=seq(*[(False, "HTTP 502")] * 3), sleep=sleeps.append)
        self.assertFalse(up)
        self.assertEqual(detail, "HTTP 502")
        self.assertEqual(sleeps, [20, 20])  # паузы только между попытками


class Decide(unittest.TestCase):
    def test_падение_без_открытого_issue_сообщить(self):
        self.assertEqual(uptime.decide(False, None), "open")

    def test_всё_ещё_лежит_молчать(self):
        self.assertIsNone(uptime.decide(False, {"number": 1}))

    def test_поднялся_сообщить_о_восстановлении(self):
        self.assertEqual(uptime.decide(True, {"number": 1}), "close")

    def test_работает_и_работал_молчать(self):
        self.assertIsNone(uptime.decide(True, None))


if __name__ == "__main__":
    unittest.main()
