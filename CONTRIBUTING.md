# Contributing to Flock

Thanks for your interest in Flock! Bug reports and feature requests are welcome
as [issues](https://github.com/flock-php/flock/issues/new/choose). For questions and help, use
[Discussions](https://github.com/flock-php/flock/discussions) or
[Discord](https://discord.flockphp.com). Please report security issues privately, as described in
the [security policy](https://github.com/flock-php/.github/blob/main/SECURITY.md).

## Pull requests start with an issue

To make sure work goes into changes that fit the project, every pull request starts with an issue in this repository:

1. **Open an issue** describing the bug or idea, or comment on an existing one.
2. **Wait for approval.** New issues are marked **`needs triage`** while they are reviewed. An approved issue is either
    - labeled **`help wanted`**, or
    - **assigned to you**.
3. **Open a pull request** against `main` that links the issue with a closing keyword, such as `Fixes #123`, or in the Development sidebar.

An issue assigned to someone else isn't available, even if it still has the `help wanted` label. If an unassigned
`help wanted` issue receives more than one pull request, the first pull request Flock processes claims it. Flock adds
the **`claimed`** label to the issue and pull request, removes `help wanted`, and closes the other pull requests.

Pull requests that don't reference an approved issue are closed automatically, with a note explaining why. Once the
issue is approved, edit the pull request's description to reference it and it reopens by itself.

Flock's prebuilt PHP and services are built in [flock-php/binaries](https://github.com/flock-php/binaries), and a
change often needs work in both repositories. When a change needs an update there, open the related issue and pull
request in `flock-php/binaries` too.

## User interface changes

Flock has native apps for macOS, Windows, and Linux. A user interface change must include the matching change on all
three platforms. If the related issue makes a change appropriate on the other platforms, a maintainer may ask you to
complete it before the pull request can merge. A pull request that does not implement the change across all three
platforms may be rejected.

## Documentation

The documentation at [flockphp.com/docs](https://flockphp.com/docs/quick-start) lives in [`docs/`](docs). A pull request
that changes only files in `docs/`, such as a typo fix or a clearer explanation, needs no issue: use **Edit this page on
GitHub** at the bottom of a page, or open a pull request against `main` directly. If it later changes files outside
`docs/` too, it needs an approved issue like any other.

The website shows the documentation of released versions only, so a change appears there with the
next release. A pull request that adds or changes a feature should update its documentation too.
