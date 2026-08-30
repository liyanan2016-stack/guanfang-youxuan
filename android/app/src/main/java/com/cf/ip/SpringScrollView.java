package com.cf.ip;

import android.animation.Animator;
import android.animation.AnimatorListenerAdapter;
import android.animation.ValueAnimator;
import android.content.Context;
import android.util.AttributeSet;
import android.view.MotionEvent;
import android.view.View;
import android.widget.ScrollView;

/**
 * 带橡皮筋回弹的 ScrollView。
 *
 * <h3>为什么要自己写</h3>
 * <p>系统默认的越界反馈分两种，两种都不合这个界面：
 * <ul>
 *   <li>API 31 以下是 {@code EdgeEffect} 发光——一道半透明色块糊在内容上，
 *       和玻璃质感冲突，而且颜色跟 colorPrimary 绑死。</li>
 *   <li>API 31 及以上是拉伸（stretch），会把卡片本身拉变形。玻璃卡片有
 *       1dp 亮描边，被拉伸后描边粗细不均，很明显是被拽歪了。</li>
 * </ul>
 * 这里改成整块内容跟手位移、松手弹回。内容让开之后露出的是底层那几团
 * 背景光斑，正好符合「玻璃浮在光上」这个设定。
 *
 * <h3>为什么钩 overScrollBy 而不是 onTouchEvent</h3>
 * <p>在 {@code onTouchEvent} 里自己算位移的话，我们消费掉的那些 MOVE 事件
 * 父类看不到，父类内部记的 {@code mLastMotionY} 会变成旧值——等交还控制权时
 * 内容会突然跳一截。{@code overScrollBy} 是父类算完滚动量之后调的钩子，
 * 此时它的内部状态已经更新过，接手不会失同步。
 */
public class SpringScrollView extends ScrollView {

    /** 最大可拉出的距离（dp）。给太大就变成能把内容拽到屏幕外了。 */
    private static final float MAX_PULL_DP = 120f;

    /** 回弹时长。比常规过渡略长一点，弹性才感觉得出来。 */
    private static final int SPRING_MS = 420;

    private float maxPull;
    /** 当前越界位移，正数=内容往下让（在顶部下拉），负数=在底部上拉。 */
    private float pull;
    private ValueAnimator springAnim;

    /** 滚动位置变化回调，用来让顶栏在内容滚动后浮出分隔线。 */
    public interface OnScrollProgressListener {
        void onScrollProgress(int scrollY);
    }

    private OnScrollProgressListener progressListener;

    public SpringScrollView(Context context) {
        this(context, null);
    }

    public SpringScrollView(Context context, AttributeSet attrs) {
        super(context, attrs);
        maxPull = MAX_PULL_DP * getResources().getDisplayMetrics().density;
        // 关掉系统的发光/拉伸，位移由我们自己做，两套叠着会互相打架
        setOverScrollMode(OVER_SCROLL_NEVER);
    }

    public void setOnScrollProgressListener(OnScrollProgressListener l) {
        this.progressListener = l;
    }

    @Override
    protected void onScrollChanged(int l, int t, int oldl, int oldt) {
        super.onScrollChanged(l, t, oldl, oldt);
        if (progressListener != null) {
            progressListener.onScrollProgress(t);
        }
    }

    @Override
    protected boolean overScrollBy(int deltaX, int deltaY, int scrollX, int scrollY,
                                   int scrollRangeX, int scrollRangeY,
                                   int maxOverScrollX, int maxOverScrollY,
                                   boolean isTouchEvent) {
        // 只接手手指拖动。惯性滑动（fling）到边界时不做位移——那会让每次
        // 快滑都以一下弹跳收尾，翻几屏就开始晕。
        if (!isTouchEvent) {
            return super.overScrollBy(deltaX, deltaY, scrollX, scrollY,
                    scrollRangeX, scrollRangeY, maxOverScrollX, maxOverScrollY, false);
        }

        boolean beyondTop = scrollY + deltaY < 0;
        boolean beyondBottom = scrollY + deltaY > scrollRangeY;

        // pull != 0 时必须继续接手，直到位移收回 0：否则手指还没回到原位，
        // 内容就已经开始正常滚动，两段动作会错开
        if (beyondTop || beyondBottom || pull != 0f) {
            cancelSpring();
            // 越拉越沉：阻尼随已拉出的距离衰减，到 maxPull 就彻底拉不动了。
            // 固定阻尼的话手感是「滑腻」，没有橡皮筋那种张力。
            float resist = 1f - Math.min(Math.abs(pull) / maxPull, 1f);
            float next = pull - deltaY * 0.5f * resist;

            // 穿过零点就停在零点，把控制权交回正常滚动。
            // 这里会丢掉一帧里的一点位移量，感知不到。
            if (pull != 0f && Math.signum(next) != Math.signum(pull)) {
                next = 0f;
            }
            pull = clamp(next);
            applyPull();
            return true;
        }

        return super.overScrollBy(deltaX, deltaY, scrollX, scrollY,
                scrollRangeX, scrollRangeY, maxOverScrollX, maxOverScrollY, true);
    }

    @Override
    public boolean onTouchEvent(MotionEvent ev) {
        switch (ev.getActionMasked()) {
            case MotionEvent.ACTION_DOWN:
                cancelSpring();
                break;
            case MotionEvent.ACTION_UP:
            case MotionEvent.ACTION_CANCEL:
                if (pull != 0f) {
                    springBack();
                    // 松手时正处在越界状态，这一下不该再触发 fling，
                    // 否则弹回动画和惯性滚动会同时改位置
                    super.onTouchEvent(MotionEvent.obtain(ev.getDownTime(), ev.getEventTime(),
                            MotionEvent.ACTION_CANCEL, ev.getX(), ev.getY(), ev.getMetaState()));
                    return true;
                }
                break;
            default:
                break;
        }
        return super.onTouchEvent(ev);
    }

    private float clamp(float v) {
        if (v > maxPull) return maxPull;
        if (v < -maxPull) return -maxPull;
        return v;
    }

    private void applyPull() {
        View child = getChildAt(0);
        if (child != null) {
            child.setTranslationY(pull);
        }
    }

    private void springBack() {
        cancelSpring();
        springAnim = ValueAnimator.ofFloat(pull, 0f);
        springAnim.setDuration(SPRING_MS);
        // 末端过冲再回落，这就是「弹」的来源；纯减速曲线只是慢慢挪回去
        springAnim.setInterpolator(Anim.EASE_OUT_BACK);
        springAnim.addUpdateListener(a -> {
            pull = (float) a.getAnimatedValue();
            applyPull();
        });
        springAnim.addListener(new AnimatorListenerAdapter() {
            @Override
            public void onAnimationEnd(Animator animation) {
                pull = 0f;
                applyPull();
            }
        });
        springAnim.start();
    }

    private void cancelSpring() {
        if (springAnim != null && springAnim.isRunning()) {
            springAnim.cancel();
        }
        springAnim = null;
    }
}
