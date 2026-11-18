//! Быстрые команды без модели: «стоп», команды Алисе (таймеры, будильники, музыка), короткие команды
//! умного дома. Распознаются по расшифровке речи и выполняются сразу.

use regex_lite::Regex;
use std::sync::LazyLock;

const LEAD: &str = r"^(?:(?:эй|слушай|так|ну)\s+)?(?:джарвис\s+)?";
const L: &str = "[а-яa-z]";

macro_rules! re {
    ($name:ident, $e:expr) => {
        static $name: LazyLock<Regex> = LazyLock::new(|| Regex::new(&$e).unwrap());
    };
}

re!(FAST_CMD, format!(r"{LEAD}(включи|выключи|погаси|отключи|зажги|запусти сценарий|запусти|останови)\s+(.+?)(?:\s+пожалуйста)?$"));
re!(COLOR, format!(
    r"{LEAD}(?:сделай|поставь|включи|переключи|измени|смени)\s+(?:цвет\s+)?(.+?)\s+(?:на\s+)?(красн|оранжев|желт|зелен|голуб|син|фиолетов|розов|бел|тепл|холодн|нейтральн){L}*(?:\s+(?:цвет|свет){L}*)?$"
));
re!(LEAD_RE, LEAD);
// «стоп» — остановить всё (и музыку); «хватит», «закрой чат», «пока»… — закончить только разговор
re!(STOP, concat!(
    r"^(?:(?:джарвис|ну|так|все|всё|спасибо)\s+)*(?:стоп|хватит|замолчи|заткнись|остановись|отбой|тихо|умолкни|прекрати|достаточно|пока|",
    r"(?:закрой|закончи|заверши|выключи)\s+(?:чат|разговор|диалог)|на этом все|можешь идти|свободен)(?:\s+(?:джарвис|все|пожалуйста|спасибо))*$"
));
re!(STOP_ALL, r"(?:^|\s)(?:стоп|остановись|прекрати)(?:\s|$)");
re!(HOME_WORDS, r"(свет|ламп|люстр|лент|торшер|пылесос|пылик|робот|кондиц|кондей|кондер|розетк|увлажнит|очистит|чайник|телевизор|яркост|сценари|штор|занавес|обогрев|вентилятор|температур)");
re!(HOME_VERBS, format!(
    r"{LEAD}(?:включи|выключи|погаси|отключи|зажги|запусти|останови|сделай|поставь|убавь|прибавь|увеличь|уменьши|открой|закрой|отправь|верни)\s"
));
// «таймер на минуту», «будильник на семь» — без глагола тоже просьба к Алисе
re!(ALICE_VERBS, r"^(?:включи|выключи|поставь|заведи|запусти|отмени|убери|удали|останови|сделай|прибавь|убавь|продолжи|напомни|разбуди|засеки|сколько осталось|какие|что (?:сейчас )?играет|громче|потише|тише|пауза|дальше|следующ|таймер|будильник)");
re!(ALICE_THINGS, r"(таймер|будильник|напомни|напоминани|разбуди|засеки|музык|песн|трек|плейлист|радио|подкаст|сказк|звуки|громкост|громче|потише|тише|на паузу|пауза|играет|блютуз|bluetooth)");
re!(MUSIC_PLAY, r"^(?:включи|поставь|заведи|запусти)(?:\s|$)");
re!(MUSIC_WORDS, r"(музык|песн|трек|плейлист|радио|подкаст|альбом)");
re!(NOT_MUSIC, r"(таймер|будильник|напомни|напоминани|разбуди|засеки|громкост|громче|потише|тише|пауз|блютуз|bluetooth)");

/// Нижний регистр, ё → е, без знаков препинания, одиночные пробелы.
pub fn norm_text(s: &str) -> String {
    let s = s.to_lowercase().replace('ё', "е");
    let cleaned: String = s.chars().map(|c| if c.is_alphanumeric() || c.is_whitespace() { c } else { ' ' }).collect();
    cleaned.split_whitespace().collect::<Vec<_>>().join(" ")
}

pub fn is_stop(text: &str) -> bool {
    STOP.is_match(&norm_text(text))
}

/// «стоп»: остановить и разговор, и музыку. Остальные стоп-фразы закрывают только разговор.
pub fn is_stop_all(text: &str) -> bool {
    let n = norm_text(text);
    STOP.is_match(&n) && STOP_ALL.is_match(&n)
}

/// Чистая команда умного дома («выключи свет», «сделай лампу синей»).
pub fn home_like(n: &str) -> bool {
    FAST_CMD.is_match(n) || COLOR.is_match(n)
}

pub fn strip_lead(n: &str) -> String {
    LEAD_RE.replace(n, "").into_owned()
}

/// Короткий звук «готово» / «не вышло», 16 кГц.
pub fn earcon(ok: bool) -> Vec<i16> {
    use std::f64::consts::PI;
    let tones: &[(f64, f64)] = if ok { &[(880.0, 0.07), (1320.0, 0.09)] } else { &[(660.0, 0.09), (440.0, 0.14)] };
    let rate = 16000.0;
    let mut out = vec![];
    for &(f, d) in tones {
        let n = (d * rate) as usize;
        for i in 0..n {
            let env = (i.min(n - i) as f64 / (0.008 * rate)).min(1.0);
            out.push((9000.0 * env * (2.0 * PI * f * i as f64 / rate).sin()) as i16);
        }
        out.extend(std::iter::repeat_n(0, 16000 / 50));
    }
    out.extend(std::iter::repeat_n(0, 16000 / 10));
    out
}

/// Просьба включить музыку (её Алиса включает из Яндекс Музыки), а не таймер или громкость.
pub fn alice_music_request(bare: &str) -> bool {
    MUSIC_PLAY.is_match(bare) && MUSIC_WORDS.is_match(bare) && !NOT_MUSIC.is_match(bare)
}

/// Просьба из тех, что выполняет только Алиса: вернуть её без имени, чтобы отдать Алисе.
pub fn alice_skill(text: &str) -> Option<String> {
    let bare = strip_lead(&norm_text(text));
    let n = bare.split_whitespace().count();
    if n == 0 || n > 12 {
        return None;
    }
    (bare == "дальше" || (ALICE_VERBS.is_match(&bare) && ALICE_THINGS.is_match(&bare))).then_some(bare)
}

/// Как alice_skill, но если Яндекс Музыка выключена, музыку Алисе не отдаём: Джарвис включит с YouTube.
pub fn alice_skill_for(text: &str, yandex_music: bool) -> Option<String> {
    alice_skill(text).filter(|b| yandex_music || !alice_music_request(b))
}

/// Короткая команда умного дома без обращения к каталогу устройств — её можно отдать Алисе целиком.
pub fn home_command_local(text: &str) -> Option<String> {
    let n = norm_text(text);
    let bare = strip_lead(&n);
    if bare.is_empty() || bare.split_whitespace().count() > 9 {
        return None;
    }
    (home_like(&n) || (HOME_VERBS.is_match(&format!("{n} ")) && HOME_WORDS.is_match(&n))).then_some(bare)
}

// ---- имя в расшифровке ----
// Проверочная расшифровка часто слышит «Джарвис» иначе: «Джарис», «Чарлис», «Жарови», «Дарвис».

/// Куски, которые бывают только в самом имени: ищем их в любом месте фразы.
const NAME_SURE: &[&str] = &["арвис", "арвес", "джарв", "жарв", "чарв", "дарв", "ярвис", "джерв", "jarv"];

pub fn edit_distance(a: &str, b: &str) -> usize {
    let (ra, rb): (Vec<char>, Vec<char>) = (a.chars().collect(), b.chars().collect());
    let mut prev: Vec<usize> = (0..=rb.len()).collect();
    for i in 1..=ra.len() {
        let mut cur = vec![i; rb.len() + 1];
        for j in 1..=rb.len() {
            let cost = usize::from(ra[i - 1] != rb[j - 1]);
            cur[j] = (prev[j] + 1).min(cur[j - 1] + 1).min(prev[j - 1] + cost);
        }
        prev = cur;
    }
    prev[rb.len()]
}

/// Слово похоже на «Джарвис» (для первых слов фразы, где имя и должно стоять).
pub fn name_like(w: &str) -> bool {
    let w = w.to_lowercase().replace('ž', "zh").replace('š', "sh");
    if ["jarvis", "jervis", "zharvis", "charvis", "djarvis", "dzharvis", "zarvis"].contains(&w.as_str()) {
        return true;
    }
    if (w.starts_with("jar") || w.starts_with("djar") || w.starts_with("zhar")) && edit_distance(&w, "jarvis") <= 2 {
        return true;
    }
    if NAME_SURE.iter().any(|s| w.contains(s)) {
        return true;
    }
    if ["джар", "жар", "чарлис", "джер", "жерв"].iter().any(|p| w.starts_with(p)) {
        return true;
    }
    let n = w.chars().count();
    (5..=9).contains(&n) && edit_distance(&w, "джарвис") <= 2
}

/// Номер слова-имени во фразе: точные куски имени — где угодно, похожие слова — только в первых двух.
pub fn name_pos(words: &[&str]) -> Option<usize> {
    words.iter().position(|w| NAME_SURE.iter().any(|s| w.contains(s))).or_else(|| words.iter().take(2).position(|w| name_like(w)))
}

pub fn has_name(text: &str) -> bool {
    let n = norm_text(text);
    name_pos(&n.split_whitespace().collect::<Vec<_>>()).is_some()
}

/// Та же фраза, но имя записано как «джарвис»: быстрые команды знают только правильное написание.
pub fn canon_name(text: &str) -> String {
    let n = norm_text(text);
    let mut words: Vec<&str> = n.split_whitespace().collect();
    if let Some(i) = name_pos(&words) {
        words[i] = "джарвис";
    }
    words.join(" ")
}

/// Детектор уверен, а расшифровка вместо имени услышала другое слово («Сейчас отключи свет»):
/// если остаток фразы — чистая команда или «стоп», считаем, что звали.
pub fn command_for_phrase(text: &str, phrase: &str) -> String {
    let normal = norm_text(text);
    let name = norm_text(phrase);
    if name == norm_text(crate::wake_profiles::DEFAULT_PHRASE) {
        return canon_name(text);
    }
    if name.is_empty() {
        return text.into();
    }
    let lead = ["эй ", "слушай ", "так ", "ну "].iter()
        .find_map(|lead| normal.strip_prefix(lead)).unwrap_or(&normal);
    if lead == name {
        return String::new();
    }
    if let Some(rest) = lead.strip_prefix(&name).and_then(|s| s.strip_prefix(' ')) {
        return rest.into();
    }
    if let Some(rest) = normal.strip_suffix(&name).and_then(|s| s.strip_suffix(' ')) {
        if is_stop(rest) {
            return rest.into();
        }
    }
    text.into()
}
pub fn command_after_lost_name(text: &str) -> Option<String> {
    let n = norm_text(text);
    let words: Vec<&str> = n.split_whitespace().collect();
    if words.len() < 2 {
        return None;
    }
    let rest = words[1..].join(" ");
    (home_like(&rest) || STOP.is_match(&rest)).then(|| format!("джарвис {rest}"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn stops_and_alice() {
        assert!(is_stop("Джарвис, стоп!"));
        assert!(is_stop_all("стоп"));
        assert!(is_stop("хватит") && !is_stop_all("хватит"));
        assert!(is_stop("закрой чат"));
        assert!(!is_stop("стоп-кран это что"));
        assert_eq!(alice_skill("Джарвис, поставь таймер на 5 минут").as_deref(), Some("поставь таймер на 5 минут"));
        assert_eq!(alice_skill_for("включи музыку", false), None);
        assert!(alice_skill_for("включи музыку", true).is_some());
        assert!(alice_skill("расскажи анекдот").is_none());
        assert_eq!(home_command_local("Джарвис, выключи свет на кухне").as_deref(), Some("выключи свет на кухне"));
        assert!(home_command_local("сделай лампу синей").is_some());
    }

    #[test]
    fn custom_phrase_is_stripped_only_at_command_boundary() {
        assert_eq!(command_for_phrase("Эй, Привет Орион, выключи свет", "Привет Орион"), "выключи свет");
        assert_eq!(command_for_phrase("Расскажи про Орион", "Орион"), "Расскажи про Орион");
        assert_eq!(command_for_phrase("Стоп, Орион", "Орион"), "стоп");
        assert_eq!(command_for_phrase("Ориончики с Марса", "Орион"), "Ориончики с Марса");
        assert_eq!(command_for_phrase("Чарлис, выключи свет", "Джарвис"), "джарвис выключи свет");
    }

    #[test]
    fn names() {
        assert!(has_name("Чарлис, выключи лампу"));
        assert_eq!(canon_name("Чарлис, выключи лампу"), "джарвис выключи лампу");
        assert!(!has_name("на улице сегодня жарко"));
        assert_eq!(command_after_lost_name("Сейчас отключи свет").as_deref(), Some("джарвис отключи свет"));
        assert_eq!(earcon(true).len(), 1120 + 1440 + 640 + 1600);
    }
}
