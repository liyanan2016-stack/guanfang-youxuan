package com.cf.ip;

import android.content.Context;
import android.graphics.RenderEffect;
import android.graphics.Shader;
import android.os.Build;
import android.util.AttributeSet;
import android.widget.ImageView;

/**
 * 背景光斑层，在支持的系统上加一次真实高斯模糊。
 *
 * <p>液态玻璃的关键不在卡片，而在卡片底下有没有东西可透。这个 View 铺的是
 * {@code page_glow} 那三团彩色光斑；模糊之后光斑边界化开，透上来的颜色是
 * 连续变化的，这才有"液态"的观感。
 *
 * <p>{@link RenderEffect} 是 Android 12（API 31）才有的。低版本直接不模糊 ——
 * 径向渐变本身已经足够柔和，只是过渡没那么润。不弹提示、不降级到别的实现：
 * 一个视觉效果不值得为它引入第三方模糊库，更不值得让低端机去跑
 * CPU 模糊拖慢滚动。
 */
public class GlowBackgroundView extends ImageView {

    public GlowBackgroundView(Context context) {
        this(context, null);
    }

    public GlowBackgroundView(Context context, AttributeSet attrs) {
        super(context, attrs);
        setScaleType(ScaleType.FIT_XY);
        applyBlur();
    }

    private void applyBlur() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
            return;
        }
        // 半径按屏幕密度给，不然高 DPI 屏上模糊会显得太弱。
        // 60dp 是试出来的量级：再小光斑边界仍可见，再大整片糊成一色，
        // 卡片底下就没有颜色变化可透了。
        float density = getResources().getDisplayMetrics().density;
        float radius = 60 * density;
        setRenderEffect(RenderEffect.createBlurEffect(radius, radius, Shader.TileMode.CLAMP));
    }
}
