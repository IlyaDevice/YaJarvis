#!/bin/sh
# Установка Джарвиса на Яндекс Станцию — Linux и macOS:
#   curl -fsSL https://github.com/__REPO__/releases/latest/download/install.sh | sh
# Скачивает установщик под вашу систему и запускает его: откроется страница в браузере,
# она сама найдёт колонку в сети и поставит Джарвиса.
set -eu
REPO="${JARVIS_REPO:-__REPO__}"
case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) echo "Эта система не поддерживается: $(uname -s). На Windows: irm https://github.com/$REPO/releases/latest/download/install.ps1 | iex" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "Процессор не поддерживается: $(uname -m)" >&2; exit 1 ;;
esac
name="JarvisSetup-$os-$arch"
base="https://github.com/$REPO/releases/latest/download"
dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT INT TERM
echo "Скачиваю установщик Джарвиса ($os/$arch)…"
if command -v curl >/dev/null 2>&1; then
    curl -fL --progress-bar -o "$dir/$name" "$base/$name"
    curl -fsSL -o "$dir/SHA256SUMS" "$base/SHA256SUMS" || true
else
    wget -q --show-progress -O "$dir/$name" "$base/$name"
    wget -q -O "$dir/SHA256SUMS" "$base/SHA256SUMS" || true
fi
if [ -s "$dir/SHA256SUMS" ]; then
    want="$(grep " $name\$" "$dir/SHA256SUMS" | cut -d' ' -f1)"
    if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$dir/$name" | cut -d' ' -f1)"; else got="$(shasum -a 256 "$dir/$name" | cut -d' ' -f1)"; fi
    if [ -n "$want" ] && [ "$want" != "$got" ]; then
        echo "Контрольная сумма не совпала — файл повреждён, попробуйте ещё раз" >&2
        exit 1
    fi
fi
chmod +x "$dir/$name"
echo "Запускаю — откроется страница в браузере. Закройте это окно, когда установка закончится."
"$dir/$name"
