#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""渲染 v1.11 液态玻璃界面预览图（浅色 + 深色）。

给人看的效果确认图，不参与构建。所有数值直接抄 res/ 里的真实取值：
颜色抄 values/colors.xml 与 values-night/colors.xml，圆角抄 drawable/*.xml，
字号间距抄 layout/activity_main.xml，所以图上的比例和真机基本一致。

注意布局按实际来：取消按钮默认 visibility=gone，所以图里不画；
扫描按钮不在卡片里（直接浮在背景光斑上）。
"""
import os
from PIL import Image, ImageDraw, ImageFilter, ImageFont

W, H = 420, 900          # 逻辑 dp 画布，约等于一台 6.1 寸手机
SS = 2                   # 超采样倍数，抗锯齿

CJK = "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc"
MONO = "/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf"


def font(size, mono=False):
    return ImageFont.truetype(MONO if mono else CJK, int(size * SS))


LIGHT = dict(
    page="#E6ECF7",
    blobs=[("#1463FF", 0x5E), ("#E36C2C", 0x4F), ("#00B8A9", 0x47), ("#7C4DFF", 0x3D)],
    glass_top=(0xFF, 0xFF, 0xFF, 0xB8),
    glass_bottom=(0xFF, 0xFF, 0xFF, 0x8F),
    stroke=(0xFF, 0xFF, 0xFF, 0xC4),
    inp=(0xFF, 0xFF, 0xFF, 0x75),
    metric=(0xFF, 0xFF, 0xFF, 0x59),
    bar=(0xFF, 0xFF, 0xFF, 0xCC),
    hair=(0x1B, 0x2C, 0x55, 0x24),
    text="#111827", text2="#5E6A7D", muted="#8994A6",
    primary="#1463FF", primary2="#0D4ED4",
    success="#0E8F5A", danger="#D92D20",
    res_bg=(0xEA, 0xF3, 0xFF, 0xE0), res_border=(0xB8, 0xD3, 0xFF, 0xFF),
)

DARK = dict(
    page="#0F131A",
    blobs=[("#3D78FF", 0x8A), ("#FF8A4C", 0x6B), ("#00D6C4", 0x5C), ("#9B6BFF", 0x66)],
    glass_top=(0x2A, 0x33, 0x45, 0xC2),
    glass_bottom=(0x1C, 0x23, 0x31, 0xA8),
    stroke=(0xFF, 0xFF, 0xFF, 0x2E),
    inp=(0x14, 0x1A, 0x24, 0x8A),
    metric=(0x22, 0x2B, 0x39, 0x6B),
    bar=(0x1A, 0x20, 0x29, 0xD1),
    hair=(0xFF, 0xFF, 0xFF, 0x1F),
    text="#F4F7FB", text2="#B0BBCB", muted="#7F8A9B",
    primary="#65A1FF", primary2="#4F86DD",
    success="#45C48A", danger="#FF6B5E",
    res_bg=(0x17, 0x22, 0x35, 0xE0), res_border=(0x2A, 0x4F, 0x89, 0xFF),
)


def blob_layer(t):
    """背景光斑：四团径向渐变 + 高斯模糊，对应 page_glow.xml + GlowBackgroundView。

    四团覆盖整个页面高度 —— 只铺上半屏的话，内容不满一屏时下半屏
    会露出一片死板纯色。
    """
    base = Image.new("RGB", (W * SS, H * SS), t["page"])
    # (中心 x, 中心 y, 半径) 比例坐标，与 page_glow.xml 各 item 的偏移对应
    specs = [(0.16, 0.14, 0.78), (0.90, 0.06, 0.70),
             (0.78, 0.62, 0.80), (0.18, 0.86, 0.82)]
    for (cx, cy, r), (col, alpha) in zip(specs, t["blobs"]):
        rr_ = int(r * W * SS)
        cxp, cyp = int(cx * W * SS), int(cy * H * SS)
        layer = Image.new("RGBA", base.size, (0, 0, 0, 0))
        d = ImageDraw.Draw(layer)
        rgb = tuple(int(col[i:i + 2], 16) for i in (1, 3, 5))
        steps = 56           # 同心圆逼近径向渐变（PIL 无原生径向渐变）
        for i in range(steps, 0, -1):
            frac = i / steps
            # Android 的 radial gradient 是从 startColor 到 endColor 的线性
            # 插值，所以这里也必须用线性衰减。之前用 (1-f)**1.7 把颜色都挤在
            # 圆心附近，预览图比真机灰得多，看不出光斑铺开的样子。
            a = int(alpha * (1 - frac))
            if a <= 0:
                continue
            rad = int(rr_ * frac)
            d.ellipse([cxp - rad, cyp - rad, cxp + rad, cyp + rad], fill=rgb + (a,))
        base = Image.alpha_composite(base.convert("RGBA"), layer).convert("RGB")
    # 对应 GlowBackgroundView 的 60dp 模糊
    return base.filter(ImageFilter.GaussianBlur(46 * SS)).convert("RGBA")


def rr(draw, box, radius, fill=None, outline=None):
    draw.rounded_rectangle([c * SS for c in box], radius=radius * SS,
                           fill=fill, outline=outline, width=SS)


def vgrad(img, box, radius, top, bottom, stroke):
    """竖向渐变 + 描边的圆角块，对应 glass_card.xml / btn_primary_bg.xml。"""
    x0, y0, x1, y1 = [int(c * SS) for c in box]
    w, h = x1 - x0, y1 - y0
    grad = Image.new("RGBA", (w, h))
    gd = ImageDraw.Draw(grad)
    for y in range(h):
        f = y / max(1, h - 1)
        gd.line([(0, y), (w, y)],
                fill=tuple(int(top[i] + (bottom[i] - top[i]) * f) for i in range(len(top))))
    mask = Image.new("L", (w, h), 0)
    ImageDraw.Draw(mask).rounded_rectangle([0, 0, w - 1, h - 1],
                                           radius=radius * SS, fill=255)
    img.paste(grad, (x0, y0), mask)
    if stroke:
        ImageDraw.Draw(img, "RGBA").rounded_rectangle(
            [x0, y0, x1, y1], radius=radius * SS, outline=stroke, width=SS)


def hexrgb(h):
    return tuple(int(h[i:i + 2], 16) for i in (1, 3, 5))


def render(t):
    img = blob_layer(t)

    def dr():
        return ImageDraw.Draw(img, "RGBA")

    def text(xy, s, f, fill, anchor=None):
        dr().text((xy[0] * SS, xy[1] * SS), s, font=f, fill=fill, anchor=anchor)

    d = dr()
    # ---- 顶栏 ----
    d.rectangle([0, 0, W * SS, 64 * SS], fill=t["bar"])
    d.line([0, 64 * SS, W * SS, 64 * SS], fill=t["hair"], width=SS)
    text((18, 22), "官方优选", font(20), t["text"])
    rr(d, (268, 21, 348, 55), 14, fill=t["inp"], outline=t["hair"])
    text((308, 31), "更新数据", font(12), t["text"], anchor="ma")
    rr(d, (358, 20, 394, 56), 18, fill=t["inp"], outline=t["hair"])
    text((376, 30), "◐", font(15), t["primary"], anchor="ma")

    y = 78
    # ---- 主按钮：不套卡片，直接浮在光斑上 ----
    vgrad(img, (14, y, W - 14, y + 52), 14,
          hexrgb(t["primary"]) + (255,), hexrgb(t["primary2"]) + (255,), None)
    text((W / 2, y + 15), "开始扫描", font(16), "#FFFFFF", anchor="ma")

    y += 66
    # ---- 结果卡 ----
    ch = 244
    vgrad(img, (14, y, W - 14, y + ch), 20, t["glass_top"], t["glass_bottom"], t["stroke"])
    d = dr()
    rr(d, (32, y + 18, W - 32, y + 64), 14, fill=t["res_bg"], outline=t["res_border"])
    text((44, y + 32), "104.16.232.1:443", font(16, mono=True), t["primary"])

    metrics = [("期望带宽", "1 Mbps", t["text"]), ("实测带宽", "48.9 MB/s", t["success"]),
               ("峰值速度", "51.2 MB/s", t["text"]), ("往返延迟", "38 ms", t["text"]),
               ("数据中心", "SIN", t["text"]), ("总用时", "26.4 s", t["text"])]
    mw = (W - 64 - 12) / 2
    for i, (k, v, col) in enumerate(metrics):
        row, col_i = divmod(i, 2)
        mx = 32 + col_i * (mw + 12)
        my = y + 78 + row * 54
        rr(d, (mx, my, mx + mw, my + 46), 14, fill=t["metric"], outline=t["hair"])
        text((mx + 12, my + 7), k, font(11), t["text2"])
        text((mx + 12, my + 23), v, font(14), col)

    y += ch + 14
    # ---- 参数卡（端口/地区已折叠，SNI 不折叠留在卡内）----
    ph = 320
    vgrad(img, (14, y, W - 14, y + ph), 20, t["glass_top"], t["glass_bottom"], t["stroke"])
    d = dr()
    text((32, y + 16), "扫描参数", font(15), t["text"])
    d.line([32 * SS, (y + 44) * SS, (W - 32) * SS, (y + 44) * SS], fill=t["hair"], width=SS)

    # IP 协议：药丸分段
    text((32, y + 56), "IP 协议", font(12), t["text2"])
    rr(d, (32, y + 74, 186, y + 110), 18, fill=t["inp"], outline=t["hair"])
    rr(d, (35, y + 77, 108, y + 107), 15, fill=t["primary"])
    text((71, y + 85), "IPv4", font(13), "#FFFFFF", anchor="ma")
    text((147, y + 85), "IPv6", font(13), t["text2"], anchor="ma")

    # 期望带宽：标签+说明在左，输入框固定 104dp 靠右
    # （原来输入框独占整行 match_parent，只收 1~4 位数字，大半宽度是空的）
    text((200, y + 62), "期望带宽", font(12), t["text2"])
    text((200, y + 80), "不确定就先填 1", font(10), t["muted"])
    rr(d, (W - 136, y + 66, W - 32, y + 110), 14, fill=t["inp"], outline=t["hair"])
    text((W - 124, y + 79), "1", font(15), t["text"])
    text((W - 74, y + 82), "Mbps", font(10), t["muted"])

    d.line([32 * SS, (y + 124) * SS, (W - 32) * SS, (y + 124) * SS], fill=t["hair"], width=SS)

    # 折叠行：标题在左，当前值摘要 + 箭头在右。
    # 摘要保证折叠不藏信息 —— 不展开也知道现在测的是哪些端口 / 哪些地区。
    for i, (title, summary) in enumerate([("端口", "443"), ("落地地区", "不限")]):
        ry = y + 124 + i * 48
        text((32, ry + 15), title, font(14), t["text"])
        text((W - 58, ry + 16), summary, font(13), t["text2"], anchor="ra")
        ax, ay = W - 44, ry + 24           # 箭头，收起时朝下
        d.line([(ax - 5) * SS, (ay - 3) * SS, ax * SS, (ay + 3) * SS],
               fill=t["text2"], width=int(1.6 * SS))
        d.line([ax * SS, (ay + 3) * SS, (ax + 5) * SS, (ay - 3) * SS],
               fill=t["text2"], width=int(1.6 * SS))
        d.line([32 * SS, (ry + 48) * SS, (W - 32) * SS, (ry + 48) * SS],
               fill=t["hair"], width=SS)

    # SNI 不折叠：v1.7 起「能不能回源到你的服务器」这项校验完全依赖它
    text((32, y + 232), "你的节点域名（SNI / Host）", font(12.5), t["text"])
    text((32, y + 252), "强烈建议填写，填了才会验证 CF 能否回源到你的服务器",
         font(10), t["muted"])
    rr(d, (32, y + 272, W - 32, y + 308), 14, fill=t["inp"], outline=t["hair"])
    text((44, y + 283), "your.domain.com", font(13), t["muted"])

    # ---- 底栏 ----
    d = dr()
    d.rectangle([0, (H - 60) * SS, W * SS, H * SS], fill=t["bar"])
    d.line([0, (H - 60) * SS, W * SS, (H - 60) * SS], fill=t["hair"], width=SS)
    text((W * 0.25, H - 46), "◎", font(19), t["primary"], anchor="ma")
    text((W * 0.25, H - 22), "优选", font(11), t["primary"], anchor="ma")
    text((W * 0.75, H - 46), "☰", font(19), t["text2"], anchor="ma")
    text((W * 0.75, H - 22), "历史", font(11), t["text2"], anchor="ma")

    return img.convert("RGB").resize((W, H), Image.LANCZOS)


light, dark = render(LIGHT), render(DARK)

pad, label = 20, 34
canvas = Image.new("RGB", (W * 2 + pad * 3, H + label + pad * 2), "#20242C")
d = ImageDraw.Draw(canvas)
f = ImageFont.truetype(CJK, 18)
d.text((pad + W / 2, pad), "浅色模式", font=f, fill="#E8ECF4", anchor="ma")
d.text((pad * 2 + W * 1.5, pad), "深色模式", font=f, fill="#E8ECF4", anchor="ma")
canvas.paste(light, (pad, pad + label))
canvas.paste(dark, (pad * 2 + W, pad + label))

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "preview-v1.12-glass.png")
canvas.save(out)
print(out)
