# RR — Health-check and prepare a 2 TB WD My Passport (0x2626) on Apple Silicon macOS 27 for Time Machine

Question: map the best current (2025–2026) approaches to health-checking and then improving / preparing a portable USB hard disk on macOS (WD My Passport 0x2626, 2 TB, Apple Silicon, macOS 27; smartctl fails; f3write/f3read running; target Time Machine).

## Answer

There is no confirmed way to read SMART from this drive on Apple Silicon. WD's Drive Utilities lists macOS 26 Tahoe but states ["ARM processor is NOT supported."](https://support-en.wd.com/app/answers/detailweb/a_id/50396/~/how-to-install-and-use-wd-drive-utilities-features) The DriveDx/SAT kext needs a [driver approval, a reboot and possibly Reduced Security](https://binaryfruit.com/drivedx/usb-drive-support). The SES-based [wd-smart-reader](https://github.com/jyatesdotdev/wd-smart-reader) does not name My Passport. That makes your full write-and-read pass the practical health test: a [full write is what makes the drive remap pending sectors](https://hddfaqs.com/how-to-force-a-remap-of-sectors-reported-in-s-m-a-r-t-c5-current-pending-sector-count/). Then format the drive as [APFS (Encrypted), which Apple prefers for Time Machine](https://support.apple.com/guide/mac-help/types-of-disks-you-can-use-with-time-machine-mh15139/mac). There is [no firmware update for portable drives](https://support-en.wd.com/app/answers/detailweb/a_id/50745/~/firmware-download-and-updates-for-western-digital-internal-and-external-drives).

## Ranked actionable list

1. **Let the f3write + f3read pass finish (already running).**
   - Tool: Homebrew `f3`.
   - Proves: every LBA accepted a write and read back intact. A failed write or read, or a corrupted sector, is a disqualifier.
   - Duration on 2 TB: about 10 h total (your measured ~95 MB/s; my estimate, not from a source).
   - Privileges: no root, no kext.
   - Caveat: f3 is built to ["Fight Flash Fraud"](https://github.com/AltraMayor/f3) and is not a dedicated HDD tool. It still exercises the whole surface.
2. **Try SMART through the WD SES route: wd-smart-reader.**
   - Install: `git clone https://github.com/jyatesdotdev/wd-smart-reader && make`.
   - Privileges: ["SES access normally works as an admin user without `sudo`"](https://github.com/jyatesdotdev/wd-smart-reader), so no kext. Its [blog](https://jyates.dev/blog/wd-smart-reader) says it works on Apple Silicon, but it was tested only on an 18 TB MyBook.
   - Support for 0x2626 / My Passport is UNCONFIRMED.
   - Never run its `erase` subcommand: it formats exFAT.
   - Proves: SMART attributes, if the bridge exposes SES.
   - Duration: seconds.
3. **WD Drive Utilities (drive status, quick and full test).**
   - Install: download from WD.
   - Status: [2.1.6.434 (09/24/2026) adds "Support for macOS 27"](https://support-en.wd.com/app/answers/detailweb/a_id/29494/~/wd-drive-utilities-release-notes). The [guide states "ARM processor is NOT supported."](https://support-en.wd.com/app/answers/detailweb/a_id/50396/~/how-to-install-and-use-wd-drive-utilities-features)
   - Apple Silicon support is UNCONFIRMED and likely fails; trying costs little.
   - Proves: a SMART pass or fail verdict only.
   - Durations: a full test is several hours on 2 TB (my estimate, unverified).
4. **DriveDx + SAT SMART Driver kext (last resort).**
   - The [page lists "Require: macOS 10.9 - macOS 26."](https://binaryfruit.com/drivedx/usb-drive-support) The steps are "You need to approve driver installation", "Reboot", and "Manually physically reconnect all external drives".
   - Kexts on Apple Silicon need [Reduced Security](https://support.apple.com/en-in/guide/deployment/depa5fb8376f/web).
   - macOS 27 support is UNCONFIRMED. The jyates blog claims the kext is incompatible with WD enclosures (DISPUTED).
   - Privileges: root plus a kext. This route weakens boot security, so it is not recommended.
5. **smartctl.**
   - drivedb.h has [no 0x2626 entry](https://raw.githubusercontent.com/smartmontools/smartmontools/master/smartmontools/drivedb.h). A WD wildcard `0x1058:0x....` maps to `-d sat` (that the wildcard covers 0x2626 is my inference), and `-d sat` already fails for you.
   - The [jyates blog](https://jyates.dev/blog/wd-smart-reader) reports that every `-d` flag fails on macOS against a WD bridge.
   - Verdict: dead end without a kext.
6. **Optional alternative surface test: badblocks.**
   - Install: [`brew install e2fsprogs`](https://formulae.brew.sh/formula/e2fsprogs). It is keg-only, so run it from `$(brew --prefix e2fsprogs)/sbin/badblocks`.
   - Use: the default mode is read-only; `-w` erases data. Unmount first. Needs root for `/dev/rdiskN`.
   - It is redundant once f3 passes.
7. **Prepare for Time Machine.**
   - After f3 passes, erase the drive. In System Settings > General > Time Machine > Add Backup Disk, [choose encryption](https://support.apple.com/guide/mac-help/choose-a-backup-disk-set-encryption-options-mh11421/mac).
   - APFS is preferred. HFS+ (Journaled) is [still supported](https://support.apple.com/guide/mac-help/types-of-disks-you-can-use-with-time-machine-mh15139/mac).
   - [Exclude items](https://support.apple.com/guide/mac-help/exclude-files-from-a-time-machine-backup-mh15622/mac) such as VMs, caches and Downloads to save space.
   - Privileges: no root.

## Map

### SMART access (partial)
- WD Drive Utilities lists "macOS 26 Tahoe" yet states "ARM processor is NOT supported." It calls its drive status check "a S.M.A.R.T. failure-prediction function." ([WD](https://support-en.wd.com/app/answers/detailweb/a_id/50396/~/how-to-install-and-use-wd-drive-utilities-features)) The release notes for 2.1.6.434 add macOS 27 ([WD notes](https://support-en.wd.com/app/answers/detailweb/a_id/29494/~/wd-drive-utilities-release-notes)), unquoted at step 6. DISPUTED for Apple Silicon.
- BinaryFruit says "macOS does not support diagnosing external drives using S.M.A.R.T. technology “out of the box”". It lists "Require: macOS 10.9 - macOS 26." and an Apple Silicon kext build, 0.10.3. ([BinaryFruit](https://binaryfruit.com/drivedx/usb-drive-support))
- jyates reports: "The WD USB-SATA bridge blocks all standard SMART passthrough commands at the firmware level"; the workaround is SES via IOKit. ([blog](https://jyates.dev/blog/wd-smart-reader); [repo](https://github.com/jyatesdotdev/wd-smart-reader))
- drivedb.h has no 0x2626 entry. ([drivedb](https://raw.githubusercontent.com/smartmontools/smartmontools/master/smartmontools/drivedb.h))

### SMART thresholds (settled)
- Backblaze watches attributes 5, 187, 188, 197 and 198: "When the RAW value for one of these five attributes is greater than zero, we have a reason to investigate." ([Backblaze 2016](https://www.backblaze.com/blog/what-smart-stats-indicate-hard-drive-failures/))
- "SMART 5 (raw value), which is deemed the most important SMART value" ([Backblaze 2021](https://www.backblaze.com/blog/using-machine-learning-to-predict-hard-drive-failures/))
- For a backup disk, any nonzero pending or uncorrectable count, or a growing reallocated count, is a reason to retire it (my judgement).
- Attributes 199 (CRC errors), 191 (G-sense) and 193 (load cycles) have no thresholds in any fetched source.

### Surface test and SMR (partial)
- f3 targets flash. Only f3write and f3read run on macOS. ([f3](https://github.com/AltraMayor/f3))
- badblocks: "By default only a non-destructive read-only test is done"; `-w` "erases data!" ([man](https://ccrma.stanford.edu/planetccrma/man/man8/badblocks.8.html))
- SMR: WD support declined to answer ([WD Community](https://community.wd.com/t/wd-blue-2-5-2tb-smr-or-pmr/234882)). A community list marks the WD20SPZX as SMR ([NAS Compares](https://nascompares.com/answer/list-of-wd-cmr-and-smr-hard-drives-hdd/)). Whether your My Passport contains that drive is unverified.
- WD says DMSMR drives need "sufficient idle time ... to perform background data management tasks" ([Blocks and Files](https://www.blocksandfiles.com/2020/04/20/western-digital-smr-drives-statement/)).
- Practical effect: sustained writes such as the first Time Machine backup may slow down; leave the drive connected while idle (inference).

### Improvement (settled)
- "There is no firmware update available for portable or desktop drives." Trying to update the firmware "may damage it." ([WD](https://support-en.wd.com/app/answers/detailweb/a_id/50745/~/firmware-download-and-updates-for-western-digital-internal-and-external-drives))
- A pending sector is cleared when written: "Write something new to the sector (even zeros ...)" ([HDD FAQs](https://hddfaqs.com/how-to-force-a-remap-of-sectors-reported-in-s-m-a-r-t-c5-current-pending-sector-count/)); see also [Wikipedia](https://en.wikipedia.org/wiki/Bad_sector). f3write already does this.
- Cable, hub and sleep guidance: no source fetched (open).

### Time Machine (settled)
- "APFS or APFS Encrypted disks are the preferred format for a Time Machine backup disk." ([Apple](https://support.apple.com/guide/mac-help/types-of-disks-you-can-use-with-time-machine-mh15139/mac))
- Encryption is chosen at setup. ([Apple](https://support.apple.com/guide/mac-help/choose-a-backup-disk-set-encryption-options-mh11421/mac))
- Exclusions save space. ([Apple](https://support.apple.com/guide/mac-help/exclude-files-from-a-time-machine-backup-mh15622/mac))

## Coverage
- SMART access: partial.
- SMART thresholds: settled.
- Surface test and SMR: partial.
- Improvement: settled; power and cable guidance is open.
- Time Machine: settled; first-backup expectations are unsourced.

Digging ended: round 2 settled nothing and added no sub-area.

## Verification
7 facts checked on 3 pages (WD Drive Utilities guide, WD firmware page, wd-smart-reader repo); all 7 were confirmed. NOT ON PAGE: none (the repo was checked for a My Passport or 0x2626 mention and has none, as the map states). UNCHECKED: none. Other quotes come from the diggers' fetches and were not re-sampled.

## Open rabbit holes
- Does wd-smart-reader / SES work on 0x2626? Dug, unsettled. Test it on the drive.
- Recording technology of the drive inside the 2 TB My Passport: dug, unsettled.
- DriveDx kext on macOS 27: undug.
- Thresholds for attributes 199, 191 and 193: dug, unsettled.
- USB cable, hub, bus power and sleep guidance: undug.
- Time Machine first-backup duration on SMR: undug.
- badblocks with `/dev/rdisk` on Apple Silicon: dug, unsettled.
