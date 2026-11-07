"""Упаковывает кадры заставки и звук для приложения.

Берёт результат export.html (строки «F data:image/png…» и «S <pcm base64>»),
обрезает все кадры по общей рамке знака и пишет:

  internal/desktop/splash/frames.bin — кадры PNG подряд;
  internal/desktop/splash/sound.wav  — звук.

Формат frames.bin (всё little-endian):
  "SRSP", u16 кадров в секунду, u16 ширина, u16 высота,
  i16 сдвиг рамки x, y (в координатах кадра 1000×700),
  u16 центр пластинки x, y и радиус (в координатах обрезанного кадра),
  u16 число кадров, затем для каждого: u32 длина, байты PNG.

Запуск: python -I pack.py <export.txt> <папка проекта>
"""
import base64
import io
import struct
import sys
import wave

from PIL import Image

FPS = 50
DISC = (486, 310, 112)  # центр и радиус пластинки в кадре 1000×700 (u = 1)

src, root = sys.argv[1], sys.argv[2]
frames, pcm = [], b""
for line in open(src, encoding="utf-8"):
    if line.startswith("F data:image/png;base64,"):
        frames.append(Image.open(io.BytesIO(base64.b64decode(line[24:].strip()))).convert("RGBA"))
    elif line.startswith("S "):
        pcm = base64.b64decode(line[2:].strip())

box = None
for im in frames:
    b = im.getchannel("A").point(lambda a: 255 if a > 2 else 0).getbbox()
    if b:
        box = b if box is None else (min(box[0], b[0]), min(box[1], b[1]), max(box[2], b[2]), max(box[3], b[3]))
pad = 4
box = (max(0, box[0] - pad), max(0, box[1] - pad), min(1000, box[2] + pad), min(700, box[3] + pad))
w, h = box[2] - box[0], box[3] - box[1]

out = io.BytesIO()
out.write(b"SRSP")
out.write(struct.pack("<HHHhhHHHH", FPS, w, h, box[0], box[1], DISC[0] - box[0], DISC[1] - box[1], DISC[2], len(frames)))
total = 0
for im in frames:
    buf = io.BytesIO()
    # 256 цветов с прозрачностью: знак почти одноцветный (лайм и тёмный
    # винил), и разница на глаз не видна, а файл меньше в разы.
    im.crop(box).quantize(256, method=Image.Quantize.FASTOCTREE, dither=Image.Dither.FLOYDSTEINBERG).save(buf, "PNG", optimize=True)
    data = buf.getvalue()
    total += len(data)
    out.write(struct.pack("<I", len(data)))
    out.write(data)
open(f"{root}/internal/desktop/splash/frames.bin", "wb").write(out.getvalue())

with wave.open(f"{root}/internal/desktop/splash/sound.wav", "wb") as wv:
    wv.setnchannels(2)
    wv.setsampwidth(2)
    wv.setframerate(44100)
    wv.writeframes(pcm)

print(f"frames {len(frames)}, box {w}x{h} at {box[:2]}, png {total // 1024} KB, wav {len(pcm) // 1024} KB")
