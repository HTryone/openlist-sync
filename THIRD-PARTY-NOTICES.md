# 第三方组件与许可声明

本程序（OpenList 网盘同步 / OpenListSync）自身以 **MIT** 许可发布，全文见 [LICENSE](LICENSE)。

分发包内含或依赖下列第三方组件。**本文件必须随分发包一起分发，不得删除。**

| 组件 | 与本程序的关系 | 许可 |
|---|---|---|
| [rclone](https://github.com/rclone/rclone) v1.76.0 | 随包分发二进制（`rclone/rclone.exe`） | MIT |
| [Go](https://go.dev/) 运行时与标准库 | 编译进 `OpenListSync.exe` | BSD-3-Clause |
| [WinFsp](https://winfsp.dev/) | 外部依赖，**不随包分发**，用户自行安装 | GPLv3 + FLOSS 特别例外 |

> 本程序通过 WebDAV 协议连接你自建的 [OpenList](https://github.com/OpenListTeam/OpenList) 服务端，
> 不包含、不分发 OpenList 的任何代码。

---

## 1. rclone（随包分发，MIT）

- 文件：`rclone/rclone.exe`
- 版本：v1.76.0（官方发行版，未做任何修改）
- 来源：https://github.com/rclone/rclone
- 许可：MIT
- 备注：rclone 自身还包含若干第三方 Go 库，各自许可见 https://rclone.org/licence/

以下为 rclone 的许可全文（`COPYING`，随包另附一份于 `rclone/COPYING`）：

```
Copyright (C) 2012 by Nick Craig-Wood http://www.craig-wood.com/nick/

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

---

## 2. Go 运行时与标准库（编译进 `OpenListSync.exe`，BSD-3-Clause）

本程序由 Go 构建，可执行文件中包含 Go 运行时与标准库的代码。

- 来源：https://go.dev/ ，源码 https://github.com/golang/go
- 许可：BSD-3-Clause

```
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

---

## 3. WinFsp（外部依赖，本包不含，GPLv3 + FLOSS 特别例外）

- 组件：**WinFsp - Windows File System Proxy, Copyright (C) Bill Zissimopoulos**
- 来源：https://winfsp.dev/ ，源码仓库 https://github.com/winfsp/winfsp
- 许可：GPLv3，附 FLOSS（自由/开源软件）特别例外
- 与本程序的关系：本程序**不打包、不分发** WinFsp，也不修改它。只有「把网盘挂载成盘符」这一功能需要在
  本机安装 WinFsp（由用户自行从官网下载安装）；只做双向同步时无需安装 WinFsp，同步相关功能不受影响。

许可例外原文（节选，完整文本含 GPLv3 全文，见
https://github.com/winfsp/winfsp/blob/master/License.txt ）：

```
The WinFsp project is Copyright (C) Bill Zissimopoulos. It is licensed
under the terms of the GPLv3.

As a special exception to GPLv3, Bill Zissimopoulos grants additional
permissions to Free/Libre and Open Source Software ("FLOSS") without requiring
that such software is covered by the GPLv3.

    1. Permission to link with a platform specific version of the WinFsp DLL
    (one of: winfsp-a64.dll, winfsp-x64.dll, winfsp-x86.dll, winfsp-msil.dll).

    2. Permission to distribute unmodified binary releases of the WinFsp
    installer (as released by the WinFsp project).

    These permissions (and no other) are granted provided that the software:

    1. Is distributed under a license that satisfies the Free Software
    Definition Version 1.141 (https://www.gnu.org/philosophy/free-sw.en.html)
    or the Open Source Definition Version 1.9 (https://opensource.org/osd).

    2. Includes the copyright notice "WinFsp - Windows File System Proxy,
    Copyright (C) Bill Zissimopoulos" and a link to the WinFsp repository in
    its user-interface and any user-facing documentation.

    3. Is not linked or distributed with proprietary (non-FLOSS) software.
    [You cannot mix FLOSS and proprietary software while using WinFsp under
    this special exception.]

Commercial licensing options are also available: Please contact
Bill Zissimopoulos <billziss at navimatics.com>.
```

上述第 2 条要求的版权声明与仓库链接，本程序已在两处保留：

- 面板「网盘挂载」卡片底部的致谢行（界面）；
- [README.md](README.md) 与 [ui/README.md](ui/README.md) 的「第三方组件」小节（面向用户的文档）。

---

## 维护约定

- 升级 `rclone.exe` 时，同步更新本文件里的版本号；如上游许可变更，一并替换许可全文。
- 新增任何随包分发的第三方二进制/库/字体/前端资源时，在本文件加一节（名称、版本、来源、许可、许可全文）。
- 打包（生成 `dist/`）时确保包含：`OpenListSync.exe`、`rclone/rclone.exe`、
  `rclone/COPYING`、`LICENSE`、`THIRD-PARTY-NOTICES.md`。

_最后更新：2026-11-26_
