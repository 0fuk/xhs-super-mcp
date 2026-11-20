package xiaohongshu

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
)

// ReportAction 笔记举报动作。
// 调用方必须自己提供 feedID、xsecToken、分类文本和描述，本方法不做内容判断。
type ReportAction struct {
	page *rod.Page
}

// NewReportAction 创建举报动作。
func NewReportAction(page *rod.Page) *ReportAction {
	return &ReportAction{page: page}
}

// ReportNote 打开笔记、选择分类、填写描述并提交。
// reason 必须与页面上的分类文案完全一致。desc 不超过 200 字。
// 未看到成功提示时返回错误，不把「点过提交」当成成功。
func (a *ReportAction) ReportNote(ctx context.Context, feedID, xsecToken, reason, desc string) error {
	reason = strings.TrimSpace(reason)
	desc = strings.TrimSpace(desc)
	if reason == "" {
		return fmt.Errorf("举报分类不能为空")
	}
	if desc == "" {
		return fmt.Errorf("举报描述不能为空")
	}
	if len([]rune(desc)) > 200 {
		return fmt.Errorf("举报描述不能超过200字")
	}

	// 与 PostComment 一致：不用 Context(ctx)，避免继承 MCP 调用的短超时。
	page := a.page.Timeout(60 * time.Second)
	url := makeFeedDetailURL(feedID, xsecToken)
	logrus.Infof("打开 feed 详情页进行举报: %s", url)

	page.MustNavigate(url)
	waitFeedPageReady(page)
	humanize.Delay(ctx, humanize.AfterNavigate)

	if err := checkPageAccessible(page); err != nil {
		return err
	}

	if err := openReportDialog(ctx, page); err != nil {
		return err
	}

	if err := clickExactText(ctx, page, reason); err != nil {
		return fmt.Errorf("未找到举报分类「%s」，分类文本必须与弹窗选项完全一致: %w", reason, err)
	}
	humanize.Delay(ctx, humanize.AfterClick)

	textarea, err := page.Timeout(8 * time.Second).Element("textarea")
	if err != nil {
		return fmt.Errorf("未找到描述输入框: %w", err)
	}
	if err := humanize.Type(ctx, textarea, desc); err != nil {
		return fmt.Errorf("填写描述失败: %w", err)
	}
	humanize.Delay(ctx, humanize.AfterType)

	if err := clickExactText(ctx, page, "提交"); err != nil {
		if err2 := clickExactText(ctx, page, "确定"); err2 != nil {
			return fmt.Errorf("未找到提交按钮: %w", err)
		}
	}

	if !waitReportAck(page, 6*time.Second) {
		return fmt.Errorf("举报未确认成功：提交后未出现成功提示，feed: %s", feedID)
	}
	logrus.Infof("Report confirmed for feed %s, reason: %s", feedID, reason)
	return nil
}

func openReportDialog(ctx context.Context, page *rod.Page) error {
	if err := clickMore(ctx, page); err != nil {
		return err
	}
	humanize.Delay(ctx, humanize.AfterClick)
	time.Sleep(700 * time.Millisecond)

	if err := clickExactText(ctx, page, "举报"); err != nil {
		return fmt.Errorf("未找到举报入口: %w", err)
	}
	time.Sleep(900 * time.Millisecond)
	return nil
}

func clickMore(ctx context.Context, page *rod.Page) error {
	el, err := page.Timeout(8 * time.Second).ElementByJS(rod.Eval(`() => {
		const sels = [
			'.note-detail-mask .more',
			'.note-scroller .more',
			'.share-wrapper',
			'.share-icon',
			'div.more-btn',
			'button.more'
		];
		for (const s of sels) {
			const el = document.querySelector(s);
			if (!el) continue;
			const r = el.getBoundingClientRect();
			if (r.width > 2 && r.height > 2) return el;
		}
		const texts = new Set(['···', '...', '…', '更多']);
		const hits = Array.from(document.querySelectorAll('button, [role="button"], div, span')).filter((el) => {
			const t = (el.innerText || '').trim();
			if (!texts.has(t)) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2 && r.width < 160 && r.height < 80;
		});
		hits.sort((a, b) => {
			const ra = a.getBoundingClientRect();
			const rb = b.getBoundingClientRect();
			return ra.width * ra.height - rb.width * rb.height;
		});
		return hits[0] || null;
	}`))
	if err != nil {
		return fmt.Errorf("未找到更多按钮: %w", err)
	}
	humanize.Delay(ctx, humanize.BeforeClick)
	if err := humanize.Click(el); err != nil {
		return fmt.Errorf("点击更多按钮失败: %w", err)
	}
	return nil
}

func clickExactText(ctx context.Context, page *rod.Page, text string) error {
	el, err := page.Timeout(8 * time.Second).ElementByJS(rod.Eval(`(text) => {
		const nodes = Array.from(document.querySelectorAll('button, [role="button"], div, span, li, label, a'));
		const hits = nodes.filter((el) => {
			const t = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
			if (t !== text) return false;
			const r = el.getBoundingClientRect();
			if (r.width < 2 || r.height < 2) return false;
			const s = window.getComputedStyle(el);
			return s.display !== 'none' && s.visibility !== 'hidden' && Number(s.opacity) !== 0;
		});
		hits.sort((a, b) => {
			const ra = a.getBoundingClientRect();
			const rb = b.getBoundingClientRect();
			return ra.width * ra.height - rb.width * rb.height;
		});
		return hits[0] || null;
	}`, text))
	if err != nil {
		return err
	}
	humanize.Delay(ctx, humanize.BeforeClick)
	return humanize.Click(el)
}

func waitReportAck(page *rod.Page, timeout time.Duration) bool {
	keywords := []string{"举报成功", "已收到你的举报", "已收到", "感谢你的反馈", "感谢反馈", "提交成功", "我们会尽快处理"}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		res, err := page.Eval(`() => document.body ? document.body.innerText : ""`)
		if err == nil {
			text := res.Value.String()
			for _, kw := range keywords {
				if strings.Contains(text, kw) {
					return true
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}
