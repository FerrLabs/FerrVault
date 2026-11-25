use std::io::BufRead;

use anyhow::{Result, anyhow};

use crate::api::Choice;

pub fn pick<'a>(
    kind: &str,
    items: &'a [Choice],
    requested: Option<&str>,
    input: &mut Option<Box<dyn BufRead>>,
) -> Result<&'a Choice> {
    if items.is_empty() {
        return Err(anyhow!("no {kind}s available to this account"));
    }
    if let Some(slug) = requested {
        return items
            .iter()
            .find(|c| c.slug == slug)
            .ok_or_else(|| anyhow!("{kind} `{slug}` not found; available: {}", slugs(items)));
    }
    if let [only] = items {
        return Ok(only);
    }
    let Some(input) = input.as_mut() else {
        return Err(anyhow!(
            "multiple {kind}s available ({}); pass --{kind} <slug>",
            slugs(items)
        ));
    };
    eprintln!("ferrvault: select a {kind}:");
    for (i, c) in items.iter().enumerate() {
        match &c.name {
            Some(n) => eprintln!("  {}) {} ({n})", i + 1, c.slug),
            None => eprintln!("  {}) {}", i + 1, c.slug),
        }
    }
    eprint!("{kind} number: ");
    let mut line = String::new();
    input.read_line(&mut line)?;
    line.trim()
        .parse::<usize>()
        .ok()
        .and_then(|n| n.checked_sub(1))
        .and_then(|i| items.get(i))
        .ok_or_else(|| anyhow!("invalid {kind} selection `{}`", line.trim()))
}

fn slugs(items: &[Choice]) -> String {
    items
        .iter()
        .map(|c| c.slug.as_str())
        .collect::<Vec<_>>()
        .join(", ")
}

#[cfg(test)]
mod tests {
    use std::io::Cursor;

    use super::*;

    fn choices(slugs: &[&str]) -> Vec<Choice> {
        slugs
            .iter()
            .map(|s| Choice {
                slug: (*s).to_owned(),
                name: None,
            })
            .collect()
    }

    #[test]
    fn single_item_is_chosen_without_input() {
        let items = choices(&["only"]);
        assert_eq!(pick("org", &items, None, &mut None).unwrap().slug, "only");
    }

    #[test]
    fn many_items_without_a_terminal_demand_the_flag() {
        let items = choices(&["a", "b"]);
        let err = pick("vault", &items, None, &mut None)
            .unwrap_err()
            .to_string();
        assert!(err.contains("--vault") && err.contains("a, b"), "{err}");
    }

    #[test]
    fn flag_selects_by_slug_and_rejects_unknown() {
        let items = choices(&["a", "b"]);
        assert_eq!(pick("org", &items, Some("b"), &mut None).unwrap().slug, "b");
        let err = pick("org", &items, Some("z"), &mut None)
            .unwrap_err()
            .to_string();
        assert!(err.contains("not found") && err.contains("a, b"));
    }

    #[test]
    fn prompt_is_one_based_and_bounds_checked() {
        let items = choices(&["a", "b"]);
        let ok = Cursor::new("2\n");
        assert_eq!(
            pick(
                "org",
                &items,
                None,
                &mut Some(Box::new(ok) as Box<dyn BufRead>)
            )
            .unwrap()
            .slug,
            "b"
        );
        for bad in ["0\n", "3\n", "x\n", "\n"] {
            let mut input: Option<Box<dyn BufRead>> = Some(Box::new(Cursor::new(bad)));
            assert!(pick("org", &items, None, &mut input).is_err(), "{bad:?}");
        }
    }

    #[test]
    fn empty_list_is_an_error() {
        assert!(pick("org", &[], None, &mut None).is_err());
    }
}
