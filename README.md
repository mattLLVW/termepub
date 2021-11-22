# Terminal Epub Reader

![demo](./demo.gif)

## Features

- Read epub files directly in your terminal.
- Images rendering.
- Save reading position.

## Download and install

Pre-compiled executables are available via [Github releases](https://github.com/mattLLVW/termepub/releases/latest).

```shell
wget https://github.com/mattLLVW/termepub/releases/download/latest/termepub-linux-amd64.tar.gz
tar -zxf termepub-linux-amd64.tar.gz -C /usr/local/bin
chmod +x /usr/local/bin/termepub
```

## Build from sources

```shell
git clone https://github.com/mattLLVW/termepub.git
cd termepub
go build main.go -o termepub
```

## Usage

```shell
termepub your_book.epub
```

### Controls

Quit: q

Line Up/Down: ↑/↓

Next/Previous chapter: ←/→

Page Up/Down: ⇞/⇟