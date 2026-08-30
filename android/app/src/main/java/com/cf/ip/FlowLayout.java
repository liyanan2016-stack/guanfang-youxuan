package com.cf.ip;

import android.content.Context;
import android.util.AttributeSet;
import android.view.View;
import android.view.ViewGroup;

/**
 * 自动换行的容器。
 *
 * <p>为什么需要它：端口和地区都是一排可点选的筹码，原来放在
 * {@code HorizontalScrollView} 里靠横向滚动。TLS 模式有 6 个端口、明文 7 个、
 * 地区 6 个，窄屏一屏只显示得下三四个，而滚动条又是隐藏的、右边没有任何
 * 渐隐提示 —— 用户不知道右边还有 2053、8443、「美国」。
 *
 * <p>端口选错的后果很具体：优选出的 IP 拿去接一个跑在别的端口的节点，
 * 客户端会报 {@code io: read/write on closed pipe}。这种关键选项不该被
 * 滚动藏起来，所以改成平铺换行、一个都不藏。
 *
 * <p>只支持 {@code wrap_content} 与 {@code match_parent} 的子视图宽度，
 * 够这里用；不引入 Material 或 Flexbox 依赖。
 */
public class FlowLayout extends ViewGroup {

    /** 子视图之间的水平间距（px）。 */
    private int hGap;
    /** 行与行之间的垂直间距（px）。 */
    private int vGap;

    public FlowLayout(Context context) {
        this(context, null);
    }

    public FlowLayout(Context context, AttributeSet attrs) {
        this(context, attrs, 0);
    }

    public FlowLayout(Context context, AttributeSet attrs, int defStyleAttr) {
        super(context, attrs, defStyleAttr);
        float density = getResources().getDisplayMetrics().density;
        hGap = (int) (8 * density + 0.5f);
        vGap = (int) (8 * density + 0.5f);
    }

    /** 设置间距（dp）。渲染筹码前调用。 */
    public void setGaps(int horizontalDp, int verticalDp) {
        float density = getResources().getDisplayMetrics().density;
        hGap = (int) (horizontalDp * density + 0.5f);
        vGap = (int) (verticalDp * density + 0.5f);
        requestLayout();
    }

    @Override
    protected void onMeasure(int widthMeasureSpec, int heightMeasureSpec) {
        int widthLimit = MeasureSpec.getSize(widthMeasureSpec) - getPaddingLeft() - getPaddingRight();
        int widthMode = MeasureSpec.getMode(widthMeasureSpec);

        int lineWidth = 0;
        int lineHeight = 0;
        int totalWidth = 0;
        int totalHeight = 0;

        for (int i = 0; i < getChildCount(); i++) {
            View child = getChildAt(i);
            if (child.getVisibility() == GONE) {
                continue;
            }
            measureChild(child, widthMeasureSpec, heightMeasureSpec);
            int cw = child.getMeasuredWidth();
            int ch = child.getMeasuredHeight();

            // 放不下就换行。lineWidth 为 0 时即使超宽也留在本行，
            // 否则单个超宽的子视图会导致空行 + 永远放不下的死循环。
            if (lineWidth > 0 && lineWidth + hGap + cw > widthLimit) {
                totalWidth = Math.max(totalWidth, lineWidth);
                totalHeight += lineHeight + vGap;
                lineWidth = cw;
                lineHeight = ch;
            } else {
                lineWidth += (lineWidth > 0 ? hGap : 0) + cw;
                lineHeight = Math.max(lineHeight, ch);
            }
        }
        totalWidth = Math.max(totalWidth, lineWidth);
        totalHeight += lineHeight;

        int finalWidth = widthMode == MeasureSpec.EXACTLY
                ? MeasureSpec.getSize(widthMeasureSpec)
                : totalWidth + getPaddingLeft() + getPaddingRight();
        setMeasuredDimension(
                finalWidth,
                resolveSize(totalHeight + getPaddingTop() + getPaddingBottom(), heightMeasureSpec));
    }

    @Override
    protected void onLayout(boolean changed, int l, int t, int r, int b) {
        int widthLimit = r - l - getPaddingLeft() - getPaddingRight();
        int x = getPaddingLeft();
        int y = getPaddingTop();
        int lineHeight = 0;
        boolean firstInLine = true;

        for (int i = 0; i < getChildCount(); i++) {
            View child = getChildAt(i);
            if (child.getVisibility() == GONE) {
                continue;
            }
            int cw = child.getMeasuredWidth();
            int ch = child.getMeasuredHeight();

            if (!firstInLine && x + cw > getPaddingLeft() + widthLimit) {
                x = getPaddingLeft();
                y += lineHeight + vGap;
                lineHeight = 0;
                firstInLine = true;
            }
            child.layout(x, y, x + cw, y + ch);
            x += cw + hGap;
            lineHeight = Math.max(lineHeight, ch);
            firstInLine = false;
        }
    }
}
