//! The app's translations: English in the code, Czech and Spanish in po/cs.po and po/es.po, keyed
//! by the English text and built into the binary. The language is chosen once at start, from the
//! Language setting, else the system's (LANGUAGE, LC_ALL, LC_MESSAGES, LANG); English is the
//! fallback for anything else. A Language setting other than the system's also sets LANGUAGE, so
//! GTK, libadwaita and WebKit translate their own texts the same way; the programs the app starts
//! get the system's back (with_system_language).
//!
//! tr! translates a literal, with {name} placeholders filled from name = value pairs, which lets a
//! translation put them in another order; trn! picks a plural form for a count, {n} in the text;
//! trc! tells apart texts that are the same in English (msgctxt), e.g. "Update" as a noun. tr_noop!
//! only marks a literal for the catalogs, for constants translated later with translate().

use std::collections::HashMap;
use std::fmt::Display;
use std::sync::OnceLock;

/// The languages the app speaks: code and name in that language (never translated).
pub const LANGUAGES: [(&str, &str); 3] = [("en", "English"), ("cs", "Čeština"), ("es", "Español")];

/// The translations of the language in use; None for English.
static CATALOG: OnceLock<Option<Catalog>> = OnceLock::new();
static LANGUAGE: OnceLock<&'static str> = OnceLock::new();
/// LANGUAGE as it was before init() set it for the Language setting, None when it was unset. Not
/// set while the app follows the system.
static SYSTEM_LANGUAGE: OnceLock<Option<String>> = OnceLock::new();

struct Catalog {
    /// Each English text's translation, or a plural's forms in the language's order.
    messages: HashMap<String, Vec<String>>,
    plural: fn(u64) -> usize,
}

/// Chooses the language for this run: a code of LANGUAGES, or "system" (also "") for the system's.
/// Without it (the screenshots, tests) the app speaks English. Call it before GTK starts and while
/// the process has no other thread: it may set LANGUAGE.
pub fn init(preference: &str) {
    let language = resolve(preference, |name| std::env::var(name).ok());
    if LANGUAGES.iter().any(|(code, _)| *code == preference) {
        let _ = SYSTEM_LANGUAGE.set(std::env::var("LANGUAGE").ok());
        // SAFETY: before any thread is started (see above).
        unsafe { std::env::set_var("LANGUAGE", language) };
    }
    let _ = LANGUAGE.set(language);
    let _ = CATALOG.set(catalog(language));
}

/// The code of the language in use.
pub fn language() -> &'static str {
    LANGUAGE.get().copied().unwrap_or("en")
}

/// Hands the system's LANGUAGE to a program the app starts, through set (Some) or unset (None),
/// when the Language setting replaced it: a restarted app would take it for the system's.
pub fn with_system_language(set: impl FnOnce(Option<&str>)) {
    if let Some(system) = SYSTEM_LANGUAGE.get() {
        set(system.as_deref());
    }
}

/// The Language setting replaced the system's LANGUAGE.
pub fn overrides_system() -> bool {
    SYSTEM_LANGUAGE.get().is_some()
}

/// with_system_language for a std::process::Command.
pub fn system_language(command: &mut std::process::Command) -> &mut std::process::Command {
    with_system_language(|value| {
        match value {
            Some(value) => command.env("LANGUAGE", value),
            None => command.env_remove("LANGUAGE"),
        };
    });
    command
}

/// The language a preference stands for, with env reading the environment.
pub fn resolve(preference: &str, env: impl Fn(&str) -> Option<String>) -> &'static str {
    if let Some((code, _)) = LANGUAGES.iter().find(|(code, _)| *code == preference) {
        return code;
    }
    // gettext's order: LANGUAGE is a list, but only counts when a locale is set at all.
    let locale = ["LC_ALL", "LC_MESSAGES", "LANG"].into_iter().filter_map(&env).find(|v| !v.is_empty()).unwrap_or_default();
    let primary = |tag: &str| tag.split(['_', '-', '.', '@']).next().unwrap_or_default().to_ascii_lowercase();
    if locale.is_empty() || primary(&locale) == "c" || primary(&locale) == "posix" {
        return "en";
    }
    let list = env("LANGUAGE").unwrap_or_default();
    list.split(':')
        .chain([locale.as_str()])
        .find_map(|tag| LANGUAGES.iter().find(|(code, _)| *code == primary(tag)).map(|(code, _)| *code))
        .unwrap_or("en")
}

fn catalog(language: &str) -> Option<Catalog> {
    let (po, plural): (&str, fn(u64) -> usize) = match language {
        "cs" => (include_str!("../po/cs.po"), czech_plural),
        "es" => (include_str!("../po/es.po"), spanish_plural),
        _ => return None,
    };
    Some(Catalog { messages: parse(po), plural })
}

/// Czech: one (1), few (2–4), other (0, 5 and more).
fn czech_plural(n: u64) -> usize {
    match n {
        1 => 0,
        2..=4 => 1,
        _ => 2,
    }
}

fn spanish_plural(n: u64) -> usize {
    if n == 1 { 0 } else { 1 }
}

fn current() -> Option<&'static Catalog> {
    CATALOG.get().and_then(Option::as_ref)
}

/// A text's translation, the text itself when there is none.
pub fn translate(text: &'static str) -> &'static str {
    current().and_then(|c| c.messages.get(text)).and_then(|forms| forms.first()).map_or(text, String::as_str)
}

/// A text's translation in a context (msgctxt).
pub fn translate_in(context: &str, text: &'static str) -> &'static str {
    current().and_then(|c| c.messages.get(&key(context, text))).and_then(|forms| forms.first()).map_or(text, String::as_str)
}

/// gettext's key of a text in a context.
fn key(context: &str, text: &str) -> String {
    format!("{context}\u{4}{text}")
}

/// The form of a plural for count n.
pub fn translate_plural(n: u64, one: &'static str, other: &'static str) -> &'static str {
    let english = if n == 1 { one } else { other };
    current().and_then(|c| c.messages.get(one).and_then(|forms| forms.get((c.plural)(n)))).map_or(english, String::as_str)
}

/// A count for trn!, of any integer type.
pub fn count(n: impl TryInto<u64>) -> u64 {
    n.try_into().unwrap_or(u64::MAX)
}

/// Fills a text's {name} placeholders; other braces stay as they are.
pub fn fill(text: &str, args: &[(&str, &dyn Display)]) -> String {
    let mut out = String::with_capacity(text.len());
    let mut rest = text;
    while let Some(start) = rest.find('{') {
        out.push_str(&rest[..start]);
        let after = &rest[start + 1..];
        match after.find('}').and_then(|end| args.iter().find(|(name, _)| *name == &after[..end]).map(|(_, value)| (end, value))) {
            Some((end, value)) => {
                out.push_str(&value.to_string());
                rest = &after[end + 1..];
            }
            None => {
                out.push('{');
                rest = after;
            }
        }
    }
    out.push_str(rest);
    out
}

/// Translates a literal; with name = value pairs, fills its {name} placeholders into a String.
macro_rules! tr {
    ($text:literal) => {
        $crate::i18n::translate($text)
    };
    ($text:literal, $($name:ident = $value:expr),+ $(,)?) => {
        $crate::i18n::fill($crate::i18n::translate($text), &[$((stringify!($name), &$value as &dyn std::fmt::Display)),+])
    };
}

/// The plural form of a text for a count, with {n} and any other placeholders filled.
macro_rules! trn {
    ($one:literal, $other:literal, $n:expr $(, $name:ident = $value:expr)* $(,)?) => {{
        let n = $crate::i18n::count($n);
        $crate::i18n::fill(
            $crate::i18n::translate_plural(n, $one, $other),
            &[("n", &n as &dyn std::fmt::Display) $(, (stringify!($name), &$value as &dyn std::fmt::Display))*],
        )
    }};
}

/// A text in a context, for English texts that other languages translate differently.
macro_rules! trc {
    ($context:literal, $text:literal) => {
        $crate::i18n::translate_in($context, $text)
    };
}

/// Marks a literal for the catalogs where it can't be translated yet (a constant); translate()
/// translates it where it shows.
macro_rules! tr_noop {
    ($text:literal) => {
        $text
    };
}

pub(crate) use {tr, tr_noop, trc, trn};

/// A .po catalog's entries, each msgid (after its msgctxt, see key) with its msgstr or msgstr[i]
/// forms. Untranslated and fuzzy entries and the header (msgid "") are left out, as gettext does.
fn parse(po: &str) -> HashMap<String, Vec<String>> {
    let mut messages = HashMap::new();
    let (mut id, mut forms): (Option<String>, Vec<String>) = (None, Vec::new());
    let mut context: Option<String> = None;
    // The string a continuation line ("...") adds to: 0 the msgid, 1 msgid_plural, 2 a msgstr, 3
    // the msgctxt, 4 none (between entries).
    let mut field = 4;
    // The entry has the fuzzy flag (#, fuzzy), which comes before it.
    let mut fuzzy = false;
    let mut finish = |id: &mut Option<String>, forms: &mut Vec<String>, fuzzy: &mut bool| {
        let translated = !forms.is_empty() && forms.iter().all(|f| !f.is_empty()) && !*fuzzy;
        if let Some(id) = id.take().filter(|id| !id.is_empty() && translated) {
            messages.insert(id, std::mem::take(forms));
        }
        forms.clear();
        *fuzzy = false;
    };
    for line in po.lines().map(str::trim) {
        if line.is_empty() {
            continue;
        }
        // A comment, a msgctxt or a msgid after a msgstr starts the next entry.
        let next = line.starts_with('#') || line.starts_with("msgctxt ") || line.starts_with("msgid ");
        if next && field == 2 {
            finish(&mut id, &mut forms, &mut fuzzy);
            field = 4;
        }
        if let Some(flags) = line.strip_prefix("#,") {
            fuzzy |= flags.split(',').any(|f| f.trim() == "fuzzy");
        } else if line.starts_with('#') {
            continue;
        } else if let Some(rest) = line.strip_prefix("msgctxt ") {
            context = Some(unquote(rest));
            field = 3;
        } else if let Some(rest) = line.strip_prefix("msgid ") {
            id = Some(unquote(rest));
            field = 0;
        } else if line.starts_with("msgid_plural ") {
            if let (Some(text), Some(context)) = (id.as_mut(), context.take()) {
                *text = key(&context, text);
            }
            field = 1;
        } else if let Some(rest) = line.strip_prefix("msgstr") {
            if let (Some(text), Some(context)) = (id.as_mut(), context.take()) {
                *text = key(&context, text);
            }
            let value = rest.split_once(' ').map(|(_, v)| v).unwrap_or_default();
            forms.push(unquote(value));
            field = 2;
        } else if line.starts_with('"') {
            match field {
                0 => id.get_or_insert_default().push_str(&unquote(line)),
                2 => forms.last_mut().expect("msgstr").push_str(&unquote(line)),
                3 => context.get_or_insert_default().push_str(&unquote(line)),
                _ => {}
            }
        }
    }
    finish(&mut id, &mut forms, &mut fuzzy);
    messages
}

/// A .po string's text, without its quotes and escapes.
fn unquote(quoted: &str) -> String {
    let inner = quoted.trim().strip_prefix('"').and_then(|s| s.strip_suffix('"')).unwrap_or_default();
    let mut out = String::with_capacity(inner.len());
    let mut chars = inner.chars();
    while let Some(c) = chars.next() {
        if c != '\\' {
            out.push(c);
            continue;
        }
        match chars.next() {
            Some('n') => out.push('\n'),
            Some('t') => out.push('\t'),
            Some(other) => out.push(other),
            None => {}
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use std::collections::{BTreeSet, HashSet};
    use std::path::Path;

    use super::*;

    /// The texts the sources mark with tr!, tr_noop!, trc! and trn!: (one, Some(other)) for plurals,
    /// a trc! text under its key.
    fn marked() -> Vec<(String, Option<String>)> {
        fn walk(dir: &Path, files: &mut Vec<std::path::PathBuf>) {
            for entry in std::fs::read_dir(dir).expect("src").flatten() {
                let path = entry.path();
                if path.is_dir() {
                    walk(&path, files);
                } else if path.extension().is_some_and(|e| e == "rs") && !path.ends_with("i18n.rs") {
                    files.push(path);
                }
            }
        }
        let mut files = Vec::new();
        walk(&Path::new(env!("CARGO_MANIFEST_DIR")).join("src"), &mut files);
        let mut found = Vec::new();
        for file in files {
            let source = std::fs::read_to_string(&file).expect("source");
            let marks = ["tr!(", "tr_noop!(", "trn!(", "trc!("].map(|mark| source.match_indices(mark));
            for (i, _) in marks.into_iter().flatten() {
                // Not the end of another name, e.g. "str!(".
                if source[..i].ends_with(|c: char| c.is_alphanumeric() || c == '_') {
                    continue;
                }
                let rest = &source[i..];
                let rest = &rest[rest.find('(').expect("(") + 1..];
                let (first, rest) = literal(rest).unwrap_or_else(|| panic!("{}: tr! without a literal", file.display()));
                let second = || {
                    let rest = rest.trim_start().strip_prefix(',').expect("a second literal");
                    literal(rest).unwrap_or_else(|| panic!("{}: a second literal missing", file.display())).0
                };
                if source[i..].starts_with("trn!(") {
                    found.push((first, Some(second())));
                } else if source[i..].starts_with("trc!(") {
                    found.push((key(&first, &second()), None));
                } else {
                    found.push((first, None));
                }
            }
        }
        found
    }

    /// The Rust string literal at the start of text (after whitespace), unescaped, and the rest.
    fn literal(text: &str) -> Option<(String, &str)> {
        let text = text.trim_start().strip_prefix('"')?;
        let mut out = String::new();
        let mut chars = text.char_indices();
        while let Some((i, c)) = chars.next() {
            match c {
                '"' => return Some((out, &text[i + 1..])),
                '\\' => match chars.next()?.1 {
                    'n' => out.push('\n'),
                    't' => out.push('\t'),
                    // A line continuation: the line break and the next line's indentation go.
                    '\n' => {
                        let skip = text[i + 2..].len() - text[i + 2..].trim_start().len();
                        for _ in 0..text[i + 2..i + 2 + skip].chars().count() {
                            chars.next();
                        }
                    }
                    other => out.push(other),
                },
                c => out.push(c),
            }
        }
        None
    }

    fn placeholders(text: &str) -> BTreeSet<String> {
        let mut names = BTreeSet::new();
        let mut rest = text;
        while let Some(start) = rest.find('{') {
            rest = &rest[start + 1..];
            if let Some(end) = rest.find('}').filter(|&end| rest[..end].chars().all(|c| c.is_ascii_alphanumeric() || c == '_')) {
                names.insert(rest[..end].to_owned());
                rest = &rest[end + 1..];
            }
        }
        names
    }

    #[test]
    fn every_text_is_translated() {
        let marked = marked();
        assert!(marked.len() > 100, "found only {} texts", marked.len());
        for (language, po, forms) in [("cs", include_str!("../po/cs.po"), 3), ("es", include_str!("../po/es.po"), 2)] {
            let messages = parse(po);
            let mut missing = Vec::new();
            for (one, other) in &marked {
                let Some(translation) = messages.get(one) else {
                    missing.push(one.clone());
                    continue;
                };
                let text = one.rsplit('\u{4}').next().unwrap_or(one);
                // Texts put together with others ("· worker mode") keep the spaces at their ends.
                let edges = |s: &str| (s.len() - s.trim_start().len(), s.len() - s.trim_end().len());
                for form in translation {
                    assert_eq!(edges(form), edges(text), "{language}: the spaces around {form:?} differ from {text:?}'s");
                }
                let english = placeholders(text).union(&other.as_deref().map(placeholders).unwrap_or_default()).cloned().collect();
                match other {
                    None => {
                        assert_eq!(translation.len(), 1, "{language}: {one:?} has plural forms");
                        assert_eq!(placeholders(&translation[0]), english, "{language}: placeholders of {one:?}");
                    }
                    Some(_) => {
                        assert_eq!(translation.len(), forms, "{language}: {one:?} needs {forms} plural forms");
                        let required: BTreeSet<String> = english.iter().filter(|p| *p != "n").cloned().collect();
                        for form in translation {
                            let found = placeholders(form);
                            assert!(found.is_subset(&english) && required.is_subset(&found), "{language}: placeholders of {form:?}");
                        }
                    }
                }
            }
            assert!(missing.is_empty(), "{language} misses translations of {missing:#?}");
            let used: HashSet<&String> = marked.iter().map(|(one, _)| one).collect();
            let unused: Vec<_> = messages.keys().filter(|id| !used.contains(id)).collect();
            assert!(unused.is_empty(), "{language} translates texts the app no longer has: {unused:#?}");
        }
    }

    #[test]
    fn parses_po_entries() {
        let po = "msgid \"\"\nmsgstr \"Language: cs\\n\"\n\n#: src/a.rs\nmsgid \"Open \\\"{name}\\\"\"\nmsgstr \"\"\n\"Otevřít „{name}“\"\n\n\
                  msgid \"{n} site\"\nmsgid_plural \"{n} sites\"\nmsgstr[0] \"{n} web\"\nmsgstr[1] \"{n} weby\"\nmsgstr[2] \"{n} webů\"\n\n\
                  msgctxt \"noun\"\nmsgid \"Update\"\nmsgstr \"Aktualizace\"\n\n\
                  #, fuzzy, c-format\nmsgid \"Fuzzy\"\nmsgstr \"Nejisté\"\n\n\
                  msgid \"Untranslated\"\nmsgstr \"\"\n";
        let messages = parse(po);
        assert_eq!(messages.len(), 3);
        assert_eq!(messages[&key("noun", "Update")], ["Aktualizace"]);
        assert_eq!(messages["Open \"{name}\""], ["Otevřít „{name}“"]);
        assert_eq!(messages["{n} site"], ["{n} web", "{n} weby", "{n} webů"]);
    }

    #[test]
    fn plural_rules() {
        let czech: Vec<usize> = [0, 1, 2, 4, 5, 11, 22, 101].into_iter().map(czech_plural).collect();
        assert_eq!(czech, [2, 0, 1, 1, 2, 2, 2, 2]);
        let spanish: Vec<usize> = [0, 1, 2, 21].into_iter().map(spanish_plural).collect();
        assert_eq!(spanish, [1, 0, 1, 1]);
    }

    #[test]
    fn english_without_a_catalog() {
        assert_eq!(translate("Sites"), "Sites");
        assert_eq!(trn!("{n} site", "{n} sites", 1), "1 site");
        assert_eq!(trn!("{n} site of {owner}", "{n} sites of {owner}", 3usize, owner = "acme"), "3 sites of acme");
        assert_eq!(tr!("{name} is {state}", name = "acme", state = String::from("up")), "acme is up");
    }

    #[test]
    fn fills_placeholders() {
        assert_eq!(fill("{b} before {a}", &[("a", &1), ("b", &"two")]), "two before 1");
        assert_eq!(fill("{unknown} and {", &[("a", &1)]), "{unknown} and {");
    }

    #[test]
    fn resolves_the_language() {
        let env = |vars: &'static [(&'static str, &'static str)]| {
            move |name: &str| vars.iter().find(|(n, _)| *n == name).map(|(_, v)| v.to_string())
        };
        assert_eq!(resolve("cs", env(&[("LANG", "es_ES.UTF-8")])), "cs");
        assert_eq!(resolve("system", env(&[("LANG", "cs_CZ.UTF-8")])), "cs");
        assert_eq!(resolve("", env(&[("LANG", "es_MX.UTF-8")])), "es");
        assert_eq!(resolve("system", env(&[("LANG", "de_DE.UTF-8")])), "en");
        assert_eq!(resolve("system", env(&[("LANGUAGE", "de:es:cs"), ("LANG", "de_DE.UTF-8")])), "es");
        assert_eq!(resolve("system", env(&[("LC_ALL", "cs_CZ.UTF-8"), ("LANG", "es_ES.UTF-8")])), "cs");
        assert_eq!(resolve("system", env(&[("LC_MESSAGES", "es_ES.UTF-8"), ("LANG", "cs_CZ.UTF-8")])), "es");
        // A C locale ignores LANGUAGE, as gettext does.
        assert_eq!(resolve("system", env(&[("LANGUAGE", "cs"), ("LANG", "C.UTF-8")])), "en");
        assert_eq!(resolve("system", env(&[])), "en");
    }

    #[test]
    fn finds_marked_literals() {
        assert_eq!(literal(" \"a \\\"b\\\" \\\n      c\" rest"), Some(("a \"b\" c".to_owned(), " rest")));
    }
}
