package com.cf.ip;

import android.animation.ValueAnimator;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Paint;
import android.graphics.RectF;
import android.util.AttributeSet;
import android.view.Gravity;
import android.view.View;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.util.ArrayList;
import java.util.List;

/**
 * 分段选择控件，选中指示块会从旧位置滑到新位置。
 *
 * <h3>为什么不继续用 RadioGroup</h3>
 * <p>RadioGroup 的选中反馈只能是背景 selector 的瞬间替换——上一版在此之上
 * 加了个放大回落，但那治不了根本问题：指示块是"消失在这里、出现在那里"，
 * 中间没有过程。看起来就是"啪"一下，动效再怎么调时长都还是呆板的。
 *
 * <p>分段控件的正确做法是让指示块自己滑过去。眼睛跟着它从 IPv4 移到 IPv6，
 * 这个位移本身就说明了"选择从左边换到了右边"，不需要额外的强调动作。
 *
 * <p>顺带也消掉了一整类 bug：RadioGroup 靠 view id 追踪选中项，动态创建的
 * 子项忘了设 id 就会出现两个同时高亮（v1.13 的「输出数量」就是这么坏的）。
 * 这里选中项是一个 int 下标，没有这个隐患。
 *
 * <h3>动画曲线</h3>
 * <p>指示块用 {@link Anim#EASE_OUT_BACK} 略微过冲再回落，时长 340ms。
 * 纯减速曲线（EASE_OUT）滑过去会像块滑到底的抽屉，有一点过冲才有"咬合"感。
 * 文字颜色同步渐变，不是跟着位置突变——指示块滑到一半时两边文字都该是中间色。
 */
public class SegmentedBar extends LinearLayout {

    /** 指示块滑动时长。比常规过渡略长，位移过程才看得清。 */
    private static final int SLIDE_MS = 340;

    public interface OnSelectListener {
        void onSelect(int index);
    }

    private final Paint thumbPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final RectF thumbRect = new RectF();
    private final List<TextView> tabs = new ArrayList<>();

    private int selected = 0;
    /**
     * 指示块当前所在的位置，用浮点表示。
     *
     * <p>0.0 = 完全在第 0 格，1.0 = 完全在第 1 格，0.5 = 正在两格之间。
     * 用连续值而不是整数下标，才能画出滑动中的中间状态。
     */
    private float thumbPos = 0f;
    private ValueAnimator slideAnim;

    private int thumbColor;
    private int textColorActive;
    private int textColorIdle;
    private float cornerRadius;
    private int innerPadding;

    private OnSelectListener listener;

    public SegmentedBar(Context context) {
        this(context, null);
    }

    public SegmentedBar(Context context, AttributeSet attrs) {
        super(context, attrs);
        setOrientation(HORIZONTAL);
        setGravity(Gravity.CENTER_VERTICAL);
        // 指示块是自己画的，必须让 onDraw 跑起来。ViewGroup 默认跳过 onDraw。
        setWillNotDraw(false);

        float density = getResources().getDisplayMetrics().density;
        innerPadding = (int) (4 * density);
        setPadding(innerPadding, innerPadding, innerPadding, innerPadding);
        setBackgroundResource(R.drawable.segmented_track_bg);

        thumbColor = getContext().getColor(R.color.primary);
        textColorActive = getContext().getColor(R.color.text_on_primary);
        textColorIdle = getContext().getColor(R.color.text_secondary);
        thumbPaint.setColor(thumbColor);
    }

    public void setOnSelectListener(OnSelectListener l) {
        this.listener = l;
    }

    /**
     * 设置选项。会清掉旧的重建，所以切 TLS 模式那种"选项集合整个换掉"的
     * 场景可以直接重新调用。
     *
     * @param equalWeight true=每格等宽（IPv4/IPv6 这种），
     *                    false=按内容宽度（1/5/10 这种短标签，等宽会太散）
     */
    public void setItems(String[] labels, boolean equalWeight) {
        removeAllViews();
        tabs.clear();
        float density = getResources().getDisplayMetrics().density;

        for (int i = 0; i < labels.length; i++) {
            final int index = i;
            TextView tv = new TextView(getContext());
            tv.setText(labels[i]);
            tv.setGravity(Gravity.CENTER);
            tv.setTextSize(15f);
            tv.setTypeface(null, android.graphics.Typeface.BOLD);
            tv.setClickable(true);
            tv.setFocusable(true);
            // 不设背景：选中态完全由自己画的指示块表达。
            // 再叠一层 selector 背景会和指示块打架，滑动中出现两个高亮块。
            tv.setOnClickListener(v -> select(index, true));

            LayoutParams lp;
            if (equalWeight) {
                lp = new LayoutParams(0, LayoutParams.MATCH_PARENT, 1f);
            } else {
                lp = new LayoutParams(LayoutParams.WRAP_CONTENT, LayoutParams.MATCH_PARENT);
                tv.setPadding((int) (18 * density), 0, (int) (18 * density), 0);
            }
            tv.setLayoutParams(lp);
            addView(tv);
            tabs.add(tv);
        }

        if (selected >= tabs.size()) {
            selected = 0;
        }
        thumbPos = selected;
        applyTextColors();
        invalidate();
    }

    /** 当前选中下标。 */
    public int getSelectedIndex() {
        return selected;
    }

    /**
     * 选中某一格。
     *
     * @param animate false 用于初始化：此时控件还没测量，滑动动画没有意义
     * @param notify  是否回调监听器。程序设定初值时不该触发"用户选择了"的逻辑
     */
    public void select(int index, boolean animate) {
        select(index, animate, true);
    }

    public void setSelectedSilently(int index) {
        select(index, false, false);
    }

    private void select(int index, boolean animate, boolean notify) {
        if (index < 0 || index >= tabs.size()) return;
        if (index == selected && !animate) {
            thumbPos = index;
            applyTextColors();
            invalidate();
            return;
        }
        boolean changed = index != selected;
        selected = index;

        if (animate && changed) {
            slideThumbTo(index);
        } else {
            cancelSlide();
            thumbPos = index;
            applyTextColors();
            invalidate();
        }

        if (changed && notify && listener != null) {
            listener.onSelect(index);
        }
    }

    private void slideThumbTo(float target) {
        cancelSlide();
        slideAnim = ValueAnimator.ofFloat(thumbPos, target);
        slideAnim.setDuration(SLIDE_MS);
        // 略微过冲再回落。纯减速曲线滑过去像抽屉滑到底，有过冲才有咬合感。
        slideAnim.setInterpolator(Anim.EASE_OUT_BACK);
        slideAnim.addUpdateListener(a -> {
            thumbPos = (float) a.getAnimatedValue();
            applyTextColors();
            invalidate();
        });
        slideAnim.start();
    }

    private void cancelSlide() {
        if (slideAnim != null && slideAnim.isRunning()) {
            slideAnim.cancel();
        }
        slideAnim = null;
    }

    /**
     * 按指示块的当前位置给文字上色。
     *
     * <p>不是"选中的白、其余灰"这么简单：指示块滑到一半时它压着两格的边缘，
     * 两边文字都应该是中间色。按距离插值，颜色跟着指示块走。
     */
    private void applyTextColors() {
        for (int i = 0; i < tabs.size(); i++) {
            float distance = Math.abs(i - thumbPos);
            float t = 1f - Math.min(distance, 1f);
            tabs.get(i).setTextColor(blend(textColorIdle, textColorActive, t));
        }
    }

    private static int blend(int from, int to, float t) {
        int a = (int) (((from >>> 24) & 0xFF) + (((to >>> 24) & 0xFF) - ((from >>> 24) & 0xFF)) * t);
        int r = (int) (((from >> 16) & 0xFF) + (((to >> 16) & 0xFF) - ((from >> 16) & 0xFF)) * t);
        int g = (int) (((from >> 8) & 0xFF) + (((to >> 8) & 0xFF) - ((from >> 8) & 0xFF)) * t);
        int b = (int) ((from & 0xFF) + ((to & 0xFF) - (from & 0xFF)) * t);
        return (a << 24) | (r << 16) | (g << 8) | b;
    }

    @Override
    protected void onDraw(Canvas canvas) {
        // 指示块画在子 View 之前（onDraw 早于 dispatchDraw），
        // 所以文字会盖在它上面，不会被挡住
        if (tabs.isEmpty()) {
            super.onDraw(canvas);
            return;
        }

        int base = Math.max(0, Math.min((int) Math.floor(thumbPos), tabs.size() - 1));
        int next = Math.min(base + 1, tabs.size() - 1);
        float frac = thumbPos - base;

        View a = tabs.get(base);
        View b = tabs.get(next);
        // 位置和宽度都插值：每格宽度可能不同（1 / 5 / 10 三格宽度不等），
        // 只插值位置会让指示块在宽度突变时闪一下
        float left = a.getLeft() + (b.getLeft() - a.getLeft()) * frac;
        float width = a.getWidth() + (b.getWidth() - a.getWidth()) * frac;

        thumbRect.set(left, innerPadding, left + width, getHeight() - innerPadding);
        if (cornerRadius <= 0) {
            // 药丸形：半径取高度的一半，和轨道的圆角呼应
            cornerRadius = thumbRect.height() / 2f;
        }
        canvas.drawRoundRect(thumbRect, cornerRadius, cornerRadius, thumbPaint);
        super.onDraw(canvas);
    }

    /** 主题切换后颜色资源会变，重新取一遍。 */
    public void refreshColors() {
        thumbColor = getContext().getColor(R.color.primary);
        textColorActive = getContext().getColor(R.color.text_on_primary);
        textColorIdle = getContext().getColor(R.color.text_secondary);
        thumbPaint.setColor(thumbColor);
        applyTextColors();
        invalidate();
    }
}
