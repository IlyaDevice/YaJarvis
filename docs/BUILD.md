# Сборка YaJavis

На машине сборки нужны актуальный стабильный Rust, Zig, CMake, Ninja, POSIX shell, zip и sha256sum. Сборка под ARMv7 также использует libclang для привязок QuickJS.

```sh
rustup target add aarch64-unknown-linux-musl armv7-unknown-linux-musleabihf
VERSION=0.9.83-rs sh build.sh
cd jarvis
cargo test --locked --workspace --all-features
cargo build --locked --release -p jarvis-setup
```

`build/release` содержит ZIP-пакеты для колонки, ядра обновления, Lua-плагины и контрольные суммы. GitHub Actions собирает установщики для Windows, Linux и macOS. Для локальной сборки под одну архитектуру задайте `STATION_ARCHES=arm64` или `STATION_ARCHES=armv7`, для своего репозитория задайте `REPO=owner/repo`. Локальные ключи и настройки сборка не читает.

Установщику нужен `adb` из Android Platform Tools. Добавьте его в PATH или задайте `JARVIS_ADB` с полным путём. Для теста локального пакета задайте `JARVIS_SETUP_PAYLOAD` с путём к каталогу ZIP-файлов. После установки Станция работает самостоятельно.

| Каталог | Содержимое |
| --- | --- |
| `jarvis/core` | Rust-ядро, панель, запуск модулей и обновление |
| `jarvis/modules` | Rust-модули подключений, музыки и обучения |
| `jarvis/station-runtime` | Запуск ядра и откат обновления на колонке |
| `jarvis/setup` | Rust-установщик для компьютера |
| `jarvisd/ui`, `installer/ui` | Веб-интерфейсы ядра и установщика |
| `station` | Скрипт автозапуска и нативные аудиоадаптеры прошивки |
| `plugins` | Манифесты модулей и Lua-плагины |
| `models`, `kws` | Модели слова и инструменты подготовки обучающих данных |
