# browse

为 AI 代理设计的浏览器自动化 CLI。你的代理发出文本命令、接收文本结果 —— 无需 GUI。

单个 Go 二进制，基于 Chrome DevTools Protocol，每条命令约 100ms。

## 安装

```bash
# 一行命令（Linux/macOS）
curl -fsSL https://raw.githubusercontent.com/dotaikit/browse/main/install.sh | sh

# 或使用 Go
go install github.com/dotaikit/browse/cmd/browse@latest
```

## 工作原理

`browse` 让 AI 代理能在终端中直接控制浏览器。代理通过无障碍快照读取页面状态，通过元素引用进行交互，并在需要人工判断时把控制权交还给你。

```bash
browse serve --headed                # 启动浏览器
browse goto https://example.com
browse snapshot -i                   # 代理以文本形式查看可交互元素
browse fill @e3 "hello"             # 代理填写表单字段
browse click @e5                    # 代理点击按钮
browse handoff "please verify"      # 代理把控制权交给你
browse resume                       # 你把控制权交还给代理
```

请将你的 AI 代理（Claude Code、Codex、Cursor 等）指向 [SKILL.md](SKILL.md) 以查看完整命令参考。

## 致谢

灵感来自 Garry Tan 在 [gstack](https://github.com/garrytan/gstack) 中的 `browse` 模块。
本项目是 Go 语言的 clean-room 重实现，不共享任何代码。
