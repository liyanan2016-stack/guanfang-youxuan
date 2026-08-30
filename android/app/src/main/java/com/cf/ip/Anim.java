package com.cf.ip;

import android.animation.Animator;
import android.animation.AnimatorListenerAdapter;
import android.animation.ValueAnimator;
import android.view.View;
import android.view.ViewGroup;
import android.view.animation.Interpolator;
import android.view.animation.PathInterpolator;

/**
 * 界面动效集中在这里，避免散落在 MainActivity 的各个分支里。
 *
 * <h3>为什么全部自己写而不用 LayoutTransition / MotionLayout</h3>
 * <p>{@code android:animateLayoutChanges} 那套默认动画的时长和曲线都改不动，
 * 而且对 {@code GONE → VISIBLE} 的高度变化经常出现先跳到全高再收回的闪帧。
 * MotionLayout 要引 ConstraintLayout 依赖并重写整份布局，为了几个过渡不值得。
 *
 * <h3>曲线选择</h3>
 * <p>统一用 {@link #EASE_OUT}（快出慢入）。UI 动效的观感来自"起步快"——
 * 用户点下去立刻看到东西动，剩下的时间用来减速收尾。默认的
 * {@code AccelerateDecelerateInterpolator} 两头都慢，起手那一段像掉帧。
 *
 * <h3>时长</h3>
 * <p>都压在 320ms 以内。这个 App 的核心操作是"点扫描然后等几十秒"，
 * 界面动画只该起到交代变化的作用，长过 1/3 秒就变成挡路的东西了。
 */
final class Anim {

    private Anim() {
    }

    /** 快出慢入。三次贝塞尔 (0.2, 0, 0, 1)，接近 Material 的 emphasized decelerate。 */
    static final Interpolator EASE_OUT = new PathInterpolator(0.2f, 0f, 0f, 1f);

    /** 回弹用的曲线，末端稍微过冲再回落。 */
    static final Interpolator EASE_OUT_BACK = new PathInterpolator(0.34f, 1.56f, 0.64f, 1f);

    static final int DUR_COLLAPSE = 260;
    static final int DUR_FADE = 220;
    static final int DUR_TAP = 130;

    /**
     * 展开一个高度为 wrap_content 的容器。
     *
     * <p>做法是先用 {@code UNSPECIFIED} 量出目标高度，再把 layoutParams.height
     * 从 0 动到该值，结束后还原成 {@code WRAP_CONTENT}。必须还原：留着固定像素高度的话，
     * 之后 chip 换行数变了（比如切 TLS 模式端口从 5 个变 3 个）容器不会跟着变，
     * 会裁掉内容或者留一块空白。
     */
    static boolean expand(final View view) {
        if (isAnimating(view)) return false;
        markAnimating(view, true);
        view.setVisibility(View.VISIBLE);
        final int target = measureWrapHeight(view);

        ValueAnimator a = ValueAnimator.ofInt(0, target);
        a.setDuration(DUR_COLLAPSE);
        a.setInterpolator(EASE_OUT);
        a.addUpdateListener(an -> {
            ViewGroup.LayoutParams lp = view.getLayoutParams();
            lp.height = (int) an.getAnimatedValue();
            view.setLayoutParams(lp);
        });
        a.addListener(new AnimatorListenerAdapter() {
            @Override
            public void onAnimationEnd(Animator animation) {
                ViewGroup.LayoutParams lp = view.getLayoutParams();
                lp.height = ViewGroup.LayoutParams.WRAP_CONTENT;
                view.setLayoutParams(lp);
                markAnimating(view, false);
            }
        });
        // 内容同时淡入。只做高度动画的话，展开过程中文字是被"擦"出来的，
        // 上面几行先满不透明度出现，看着像内容从缝里挤出来。
        view.setAlpha(0f);
        view.animate().alpha(1f).setDuration(DUR_COLLAPSE).setInterpolator(EASE_OUT).start();
        a.start();
        return true;
    }

    /** 收起容器，结束后置为 GONE 并还原高度。动画已在进行中则返回 false。 */
    static boolean collapse(final View view) {
        if (isAnimating(view)) return false;
        markAnimating(view, true);
        final int from = view.getHeight();
        ValueAnimator a = ValueAnimator.ofInt(from, 0);
        a.setDuration(DUR_COLLAPSE);
        a.setInterpolator(EASE_OUT);
        a.addUpdateListener(an -> {
            ViewGroup.LayoutParams lp = view.getLayoutParams();
            lp.height = (int) an.getAnimatedValue();
            view.setLayoutParams(lp);
        });
        a.addListener(new AnimatorListenerAdapter() {
            @Override
            public void onAnimationEnd(Animator animation) {
                view.setVisibility(View.GONE);
                ViewGroup.LayoutParams lp = view.getLayoutParams();
                lp.height = ViewGroup.LayoutParams.WRAP_CONTENT;
                view.setLayoutParams(lp);
                view.setAlpha(1f);
                markAnimating(view, false);
            }
        });
        view.animate().alpha(0f).setDuration(DUR_COLLAPSE - 60).setInterpolator(EASE_OUT).start();
        a.start();
        return true;
    }

    /**
     * 折叠动画进行中的标记，挂在 view 的 tag 上。
     *
     * <p>必须防抖：{@link #collapse} 到动画结束才把 visibility 设成 GONE，
     * 这中间用户再点一次表头，{@code toggleSection} 看到的仍是 VISIBLE，
     * 于是又发起一次 collapse —— 两个 ValueAnimator 同时改 layoutParams.height，
     * 结果高度会停在中间值，那一块就永久残缺了。
     */
    private static boolean isAnimating(View view) {
        return Boolean.TRUE.equals(view.getTag(R.id.tag_anim_running));
    }

    private static void markAnimating(View view, boolean running) {
        view.setTag(R.id.tag_anim_running, running ? Boolean.TRUE : null);
    }

    /** 量出 wrap_content 下的高度。宽度按父容器实际可用宽度给，否则 chip 换行数会算错。 */
    private static int measureWrapHeight(View view) {
        ViewGroup parent = (ViewGroup) view.getParent();
        int availableWidth = parent.getWidth() - parent.getPaddingLeft() - parent.getPaddingRight();
        if (availableWidth <= 0) {
            availableWidth = view.getWidth();
        }
        int wSpec = View.MeasureSpec.makeMeasureSpec(availableWidth, View.MeasureSpec.EXACTLY);
        int hSpec = View.MeasureSpec.makeMeasureSpec(0, View.MeasureSpec.UNSPECIFIED);
        view.measure(wSpec, hSpec);
        return view.getMeasuredHeight();
    }

    /** 折叠箭头旋转。展开朝上，收起朝下。 */
    static void rotateArrow(View arrow, boolean expanded) {
        arrow.animate()
                .rotation(expanded ? 180f : 0f)
                .setDuration(DUR_COLLAPSE)
                .setInterpolator(EASE_OUT)
                .start();
    }

    /**
     * 淡入 + 轻微上移。用于结果卡、进度卡、分页切换。
     *
     * <p>位移只给 10dp 左右：位移大了会变成"飞进来"，在一个会反复出现同一张卡片的
     * 界面里很快就烦人。这里只是暗示"这块是新出现的"。
     */
    static void fadeInUp(View view, float dyPx, long delay) {
        view.setVisibility(View.VISIBLE);
        view.setAlpha(0f);
        view.setTranslationY(dyPx);
        view.animate()
                .alpha(1f)
                .translationY(0f)
                .setStartDelay(delay)
                .setDuration(DUR_FADE)
                .setInterpolator(EASE_OUT)
                .start();
    }

    /**
     * 给可点元素装上"按下缩小 + 变暗，松手回弹"。
     *
     * <p>用 OnTouchListener 而不是 StateListAnimator：后者要单独写一份
     * XML 动画资源，而且和布局里已有的 {@code stateListAnimator="@null"}
     * （为了去掉默认投影而设的）冲突。
     *
     * <p>同时压 alpha 而不是只做缩放：玻璃元素是半透明的，单靠 3% 的尺寸
     * 变化在浅色背景上几乎看不出来，配合一点变暗才有明确的"按住了"反馈。
     *
     * <p>ACTION_UP 和 ACTION_CANCEL 都要还原，否则手指滑出控件外再松手
     * 会把按钮永久留在缩小变暗的状态。
     *
     * <p>返回 false 让事件继续往下传，OnClickListener 照常触发。
     *
     * @param scale 按下时缩到多少。主按钮给 0.97（大面积元素缩太多会晃眼），
     *              小控件可以给 0.93。
     */
    static void attachPressScale(final View view, final float scale) {
        view.setOnTouchListener((v, ev) -> {
            switch (ev.getActionMasked()) {
                case android.view.MotionEvent.ACTION_DOWN:
                    v.animate().cancel();
                    v.animate().scaleX(scale).scaleY(scale).alpha(0.82f)
                            .setDuration(DUR_TAP).setInterpolator(EASE_OUT).start();
                    break;
                case android.view.MotionEvent.ACTION_UP:
                case android.view.MotionEvent.ACTION_CANCEL:
                    v.animate().cancel();
                    v.animate().scaleX(1f).scaleY(1f).alpha(1f)
                            .setDuration(DUR_TAP * 2).setInterpolator(EASE_OUT_BACK).start();
                    break;
                default:
                    break;
            }
            return false;
        });
    }

    /** 默认缩放量的重载。 */
    static void attachPressScale(final View view) {
        attachPressScale(view, 0.97f);
    }

    /**
     * 批量装配按压反馈。
     *
     * <p>逐个写 attachPressScale 会漏——这个界面上可点元素有十几个，
     * 漏掉的那几个按下去没反应，用户会以为点歪了。
     */
    static void attachPressScale(float scale, View... views) {
        for (View v : views) {
            if (v != null) attachPressScale(v, scale);
        }
    }

    /**
     * 横向滑入 + 淡入。用于分页切换。
     *
     * <p>位移只给 20dp 左右。整屏宽度的滑动（那种真正的 pager 效果）需要
     * 两个页面同时在场、一个滑出一个滑入；这里两个 ScrollView 是
     * visibility 互斥的，硬做全宽滑动会看到一片空白扫过去。20dp 的
     * 短距离位移足够交代"换了一页"，也不会露出空白。
     *
     * @param dxPx 起始横向偏移，正数=从右边进，负数=从左边进
     */
    static void slideIn(View view, float dxPx) {
        view.setVisibility(View.VISIBLE);
        view.setAlpha(0f);
        view.setTranslationX(dxPx);
        view.animate()
                .alpha(1f)
                .translationX(0f)
                .setDuration(DUR_FADE)
                .setInterpolator(EASE_OUT)
                .start();
    }

    /** 点击回弹。chip 和导航项用，给一个"按到了"的实感。 */
    static void tapBounce(View view) {
        view.animate().cancel();
        view.setScaleX(0.92f);
        view.setScaleY(0.92f);
        view.animate()
                .scaleX(1f).scaleY(1f)
                .setDuration(DUR_TAP * 2)
                .setInterpolator(EASE_OUT_BACK)
                .start();
    }

    /**
     * 一组视图依次错开淡入。
     *
     * <p>只动 alpha 和一点位移，不动 layout —— 逐个做高度动画会引发多轮
     * measure/layout，六个指标块同时跑的话中低端机上会掉帧。
     *
     * @param stepMs 相邻两个之间的延迟。40ms 左右最合适：再小看不出次序，
     *               再大最后一个要等到 300ms 以后，显得界面卡。
     */
    static void staggerIn(View[] views, long stepMs) {
        int shown = 0;
        for (View v : views) {
            // 跳过 GONE 的：结果卡和进度卡在启动时是隐藏的，给它们排上序号
            // 会让后面真正可见的块白等几十毫秒
            if (v == null || v.getVisibility() == View.GONE) continue;
            int i = shown++;
            v.setAlpha(0f);
            v.setTranslationY(8f * v.getResources().getDisplayMetrics().density);
            v.animate()
                    .alpha(1f)
                    .translationY(0f)
                    .setStartDelay(i * stepMs)
                    .setDuration(DUR_FADE)
                    .setInterpolator(EASE_OUT)
                    .start();
        }
    }

    /**
     * 分段控件里选中项的切换反馈。
     *
     * <p>新选中的那个轻微放大再回落。这比单纯换个背景色明显得多——
     * 三个 44dp 的方块只靠颜色变化，眼睛容易看不出到底换没换，
     * 而这正是「输出数量」那个 bug 在真机上难以察觉的原因之一。
     */
    static void segmentSelect(View view) {
        if (view == null) return;
        view.animate().cancel();
        view.setScaleX(0.9f);
        view.setScaleY(0.9f);
        view.animate()
                .scaleX(1f).scaleY(1f)
                .setDuration(DUR_TAP * 2)
                .setInterpolator(EASE_OUT_BACK)
                .start();
    }

    /**
     * 淡入淡出地切换一个 View 的可见性。
     *
     * <p>用于顶栏分隔线这类"随滚动出现/消失"的装饰：直接改 visibility
     * 会让线突然闪出来，比没有线更扎眼。
     *
     * <p>已经是目标状态时直接返回，否则每帧滚动回调都会重启一次动画，
     * alpha 永远停在中间值。
     */
    static void fadeTo(View view, boolean show) {
        if (view == null) return;
        float target = show ? 1f : 0f;
        if (view.getAlpha() == target) return;
        view.animate().cancel();
        view.animate().alpha(target)
                .setDuration(DUR_FADE)
                .setInterpolator(EASE_OUT)
                .start();
    }

    /** 数值刷新时的轻微脉冲，用于结果指标从旧值换成新值。 */
    static void pulse(View view) {
        if (view == null) return;
        view.animate().cancel();
        // cancel 可能掐掉一条还没跑完的淡入（staggerIn 挂的是带 startDelay 的
        // 动画，延迟阶段被 cancel 就永远不会把 alpha 拉回 1），所以这里
        // 强制复位可见性。脉冲的前提是这个值本来就该看得见。
        view.setAlpha(1f);
        view.setTranslationY(0f);
        view.setScaleX(1.06f);
        view.setScaleY(1.06f);
        view.animate()
                .scaleX(1f).scaleY(1f)
                .setDuration(DUR_FADE)
                .setInterpolator(EASE_OUT_BACK)
                .start();
    }
}
