"""Собирает значок программы из размеров, нарисованных icon.html.

Пишет internal/desktop/icon.ico (окно и трей), а рядом в
tools/splash/icon-256.png — для exe и для превью.

ICO собирается руками из PNG-картинок: так каждый размер остаётся ровно
тем, что нарисован, без пересчёта.

Запуск: python -I pack_icon.py <icon.txt> <папка проекта>
"""
import base64
import struct
import sys

src, root = sys.argv[1], sys.argv[2]
imgs = []
for line in open(src, encoding="utf-8"):
    size, data = line.split(" ", 1)
    imgs.append((int(size), base64.b64decode(data.split(",", 1)[1])))

head = struct.pack("<HHH", 0, 1, len(imgs))
dir_, body = b"", b""
offset = 6 + 16 * len(imgs)
for size, png in imgs:
    dim = 0 if size >= 256 else size
    dir_ += struct.pack("<BBBBHHII", dim, dim, 0, 0, 1, 32, len(png), offset + len(body))
    body += png
open(f"{root}/internal/desktop/icon.ico", "wb").write(head + dir_ + body)
open(f"{root}/tools/splash/icon-256.png", "wb").write(imgs[-1][1])
print("sizes", [s for s, _ in imgs], "bytes", len(head + dir_ + body))
