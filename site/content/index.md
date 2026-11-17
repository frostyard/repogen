---
title: repogen
description: repogen turns a directory of packages into a static, signed package repository that any web server can host.
hide:
  - navigation
  - toc
---

<div class="fy-hero" markdown>
<div class="fy-hero__text" markdown>
<p class="fy-eyebrow">repogen</p>

# repogen

<p class="fy-hero__lede">repogen turns a directory of packages into a static, signed package repository that any web server can host. It scans the directory, detects each package type and writes the metadata that APT, apk, pacman, Homebrew and systemd-sysupdate read. It runs on Linux and is for people who serve packages from plain web or object storage.</p>

[Install repogen →](getting-started/install.md){ .md-button .md-button--primary }
[Read the usage guide](guides/usage.md){ .md-button }

</div>
<img class="fy-hero__icon" src="assets/logo.svg" alt="">
</div>

[Install repogen](getting-started/install.md) covers the release install and the build from source.

## What repogen gives you

<div class="grid cards" markdown>

-   **Five repository formats**

    ---

    Debian/APT, Alpine/APK, Arch Linux/pacman, Homebrew bottles and systemd-sysext images. repogen finds each package by its magic bytes, with the file name as a fallback.

-   **Signed output**

    ---

    GPG signs Debian, pacman and sysext repositories, and RSA signs Alpine. Unsigned output stays possible.

-   **Incremental updates**

    ---

    Incremental mode reads the published metadata and adds new packages without removing old ones. Sysext retention is opt-in: by default nothing is pruned.

-   **Static output**

    ---

    The result is plain files. Serve it from any web server or object store, or publish it to Cloudflare R2 with the `publish-to-r2` GitHub action.

</div>

!!! note "Pre-release"

    repogen is pre-release software. RPM generation was removed in v0.7.0.

## Start here

<div class="grid cards" markdown>

- <span class="fy-index">01</span> **[Install](getting-started/install.md)**

    Install repogen from a release or build it from source.

- <span class="fy-index">02</span> **[Usage](guides/usage.md)**

    Generate, sign and update a repository.

- <span class="fy-index">03</span> **[Client setup](guides/client-setup.md)**

    Point APT, apk, pacman, Homebrew and systemd-sysupdate at the result.

- <span class="fy-index">04</span> **[Publish to R2](guides/publish-to-r2.md)**

    Publish a repository to Cloudflare R2 from GitHub Actions.

- <span class="fy-index">05</span> **[Commands](reference/cli.md)**

    Look up every flag of `repogen generate`.

- <span class="fy-index">06</span> **[Build from source](contributing/build.md)**

    Build repogen, run the checks and build this site.

</div>
