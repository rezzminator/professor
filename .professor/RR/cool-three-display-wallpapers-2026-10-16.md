# RR — Cool wallpapers for three displays

Question: Goal: find COOL wallpapers for the user's three displays, limited to the five categories they chose.

## Answer

Eight metadata-verified picks survived: [four ultrawides](https://wallhaven.cc/api/v1/w/e7dw1w), [one laptop image](https://wallhaven.cc/api/v1/w/xej7ko), and [three portraits](https://wallhaven.cc/api/v1/w/7j91ee). This is a deliberately incomplete shortlist, selected from tags and favourites rather than visual inspection.

```PICKS
ultra|e7dw1w|5120x1440|https://w.wallhaven.cc/full/e7/wallhaven-e7dw1w.jpg|deep-space|63|JWST Tarantula Nebula; source record links to Webb imagery.
ultra|289oex|5120x1440|https://w.wallhaven.cc/full/28/wallhaven-289oex.jpg|sci-fi|52|Starfield concept art tagged spaceship, planet, robot and sunset.
ultra|1q1oxg|5120x1440|https://w.wallhaven.cc/full/1q/wallhaven-1q1oxg.jpg|sci-fi|45|GTGraphics ringed gas giant, asteroids and spaceship.
ultra|k7oy6m|5120x1440|https://w.wallhaven.cc/full/k7/wallhaven-k7oy6m.png|deep-space|39|Carina Nebula / NGC 3324; source record links to NASA Webb first images.
laptop|xej7ko|3840x2400|https://w.wallhaven.cc/full/xe/wallhaven-xej7ko.jpg|sci-fi|43|Vadim Sadovski space art tagged astronaut, spacesuit and black holes.
portrait|7j91ee|2250x4000|https://w.wallhaven.cc/full/7j/wallhaven-7j91ee.png|cyberpunk|111|Idil Dursun cityscape tagged Blade Runner, skyscraper, city lights and planet.
portrait|zppjyg|2304x4096|https://w.wallhaven.cc/full/zp/wallhaven-zppjyg.jpg|cyberpunk|59|Idil Dursun futuristic city concept art with neon signs and a bridge.
portrait|kw7ok7|2160x3840|https://w.wallhaven.cc/full/kw/wallhaven-kw7ok7.png|synthwave|53|Retrowave sunset and palm trees with purple, blue, magenta and red colours.
```

## Decisions and constraints

- Delivered 4/10 ultra, 1/12 laptop and 3/8 portrait: 8/30 overall. Four of five categories represented; mechs absent. Category balance is not achieved. The user explicitly preferred fewer verified picks over padding.
- Ranked retained picks by favourites within each display. Counts are the fetched record values, not a permanent popularity ranking.
- All eight individual image records explicitly state `"purity":"sfw"`. Dimensions, direct paths and favourites were checked individually; evidence follows below.
- Ultra: all four are 5120x1440 and satisfy the requested 3.50–3.60 aspect interval. Laptop: 3840x2400 satisfies width >=3024 and aspect interval 1.45–1.65. Portrait: all three dimensions are exact 9:16 and satisfy the minimum dimensions.
- No excluded ID appears, and no image ID is reused across displays. Distinct IDs do not establish perceptual uniqueness, but the retained subject tags do not indicate obvious variant pairs.
- No images downloaded; no display settings or other machine configuration changed. Only this requested report was written.
- Taste assessment is provisional: source metadata and tags were read, not the image pixels. Descriptions above deliberately describe tagged subjects, not unverified visual quality. No claim that every pick is cinematic, detailed or epic is established by these records.

## Map and quoted record evidence

### Sci-fi concept art

Settled when two ultra, two laptop and two portrait suitable SFW picks have individually checked metadata, or the shortfall is adequately explained. **Partial:** two ultra and one laptop retained; no suitable sci-fi portrait retained separately from cyberpunk. The D.Va candidate was rejected as character-led. Capital-ship searches remain incomplete.

- [289oex record](https://wallhaven.cc/api/v1/w/289oex): `"id":"289oex"`, `"resolution":"5120x1440"`, `"purity":"sfw"`, `"favorites":52`, `"path":"https:\/\/w.wallhaven.cc\/full\/28\/wallhaven-289oex.jpg"`; tags `"name":"spaceship"`, `"name":"Starfield (video game)"`, `"name":"concept art"`. Additional returned tags include planet, robot and sunset.
- [1q1oxg record](https://wallhaven.cc/api/v1/w/1q1oxg): `"id":"1q1oxg"`, `"resolution":"5120x1440"`, `"purity":"sfw"`, `"favorites":45`, `"path":"https:\/\/w.wallhaven.cc\/full\/1q\/wallhaven-1q1oxg.jpg"`; tags `"name":"planetary rings"`, `"name":"Gas giant"`, `"name":"asteroid"`, `"name":"spaceship"`, `"name":"GTGraphics"`. Also tagged watermarked.
- [xej7ko record](https://wallhaven.cc/api/v1/w/xej7ko): `"id":"xej7ko"`, `"resolution":"3840x2400"`, `"purity":"sfw"`, `"favorites":43`, `"path":"https:\/\/w.wallhaven.cc\/full\/xe\/wallhaven-xej7ko.jpg"`; tags `"name":"astronaut"`, `"name":"black holes"`, `"name":"science fiction"`, `"name":"space art"`. The fetched record also names Vadim Sadovski and spacesuit.

### Cyberpunk / neon cities

Settled when two ultra, three laptop and two portrait suitable SFW picks have individually checked metadata, or the shortfall is adequately explained. **Partial:** two portrait cityscapes retained; no suitable ultra or laptop retained. Sci-fi search discoveries were reassigned here by subject, never duplicated.

- [7j91ee record](https://wallhaven.cc/api/v1/w/7j91ee): `"id":"7j91ee"`, `"resolution":"2250x4000"`, `"purity":"sfw"`, `"favorites":111`, `"path":"https:\/\/w.wallhaven.cc\/full\/7j\/wallhaven-7j91ee.png"`; tags `"name":"Blade Runner"`, `"name":"futuristic"`, `"name":"city lights"`, `"name":"skyscraper"`, `"name":"planet"`, `"name":"Idil Dursun"`.
- [zppjyg record](https://wallhaven.cc/api/v1/w/zppjyg): `"id":"zppjyg"`, `"resolution":"2304x4096"`, `"purity":"sfw"`, `"favorites":59`, `"path":"https:\/\/w.wallhaven.cc\/full\/zp\/wallhaven-zppjyg.jpg"`; tags `"name":"futuristic city"`, `"name":"neon sign"`, `"name":"bridge"`, `"name":"Idil Dursun"`; `"source":"https:\/\/www.artstation.com\/artwork\/Rn5q0e"`.
- Initial [cyberpunk ultra search](https://wallhaven.cc/api/v1/search?q=cyberpunk&purity=100&categories=111&sorting=toplist&topRange=1y&ratios=32x9&atleast=5120x1440) returned `"total":0`, according to the digger's quoted response. This does not establish all-time absence. Alternate keywords and all-time city searches remain open.
- Rejected 6l5emq and gwq1w3: ASCII face variants, not the requested city scenes. Rejected 6ll1rl: character-led rather than a city. The latter record's SFW purity is not contradicted merely by clothing/body-part tags; exclusion was about taste, not an established purity violation.

### Real deep space

Settled when two ultra, two laptop and one portrait real-astronomy SFW picks have checked metadata and supporting provenance, or the shortfall is adequately explained. **Partial:** two ultrawides retained with astronomy subject tags and official-origin links in their Wallhaven records. No laptop or portrait provenance established.

- [e7dw1w record](https://wallhaven.cc/api/v1/w/e7dw1w): `"id":"e7dw1w"`, `"resolution":"5120x1440"`, `"purity":"sfw"`, `"favorites":63`, `"path":"https:\/\/w.wallhaven.cc\/full\/e7\/wallhaven-e7dw1w.jpg"`; tags `"name":"James Webb Space Telescope"`, `"name":"Tarantula Nebula"`. Source field points to `https://webbtelescope.org/contents/media/images/2022/041/01GA76MYFN0FMKNRHGCAGGYCVQ?page=1&filterUUID=91dfa083-c258-4f9f-bef1-8f40c26f4c97`.
- [k7oy6m record](https://wallhaven.cc/api/v1/w/k7oy6m): `"id":"k7oy6m"`, `"resolution":"5120x1440"`, `"purity":"sfw"`, `"favorites":39`, `"path":"https:\/\/w.wallhaven.cc\/full\/k7\/wallhaven-k7oy6m.png"`; tags `"name":"Carina Nebula"`, `"name":"NGC 3324"`, `"name":"NASA"`; `"source":"https:\/\/www.nasa.gov\/webbfirstimages"`.
- Official-origin URLs were read as fields of Wallhaven records, not independently opened or compared against the wallpaper pixels. Provenance is therefore source-record evidence, not an independent authenticity audit.
- Excluded portrait leads r22ew1, 2klrm9 and mpexkk: astronomy tags without confirmed origin. Excluded laptop lead j3779m pending source identification.

### Synthwave / outrun

Settled when two ultra, three laptop and one portrait suitable SFW picks have individually checked metadata and subject tags, or the shortfall is adequately explained. **Partial:** one portrait retained; no ultra or laptop met the taste screening. Outrun filtered searches remain incomplete.

- [kw7ok7 record](https://wallhaven.cc/api/v1/w/kw7ok7): `"id":"kw7ok7"`, `"resolution":"2160x3840"`, `"purity":"sfw"`, `"favorites":53`, `"path":"https:\/\/w.wallhaven.cc\/full\/kw\/wallhaven-kw7ok7.png"`; `"name":"sunset"`, `"name":"retrowave"`, `"name":"synthwave"`, `"name":"palm trees"`; `"colors":["#663399","#424153","#333399","#993399","#cc3333"]`.
- [ml2wvm record](https://wallhaven.cc/api/v1/w/ml2wvm) confirmed 5120x1440, sfw, 56 favourites but tags include `"name":"Tux"`, `"name":"penguins"`, `"name":"simple background"`. Rejected, despite synthwave tagging.
- [2y5me6 record](https://wallhaven.cc/api/v1/w/2y5me6) confirmed 3606x2404, sfw, 96 favourites. Tags include `"name":"river"`, `"name":"satellite"`, `"name":"aerial view"`, `"name":"top view"`. Rejected as weak evidence for the requested retro scene rather than a neon aerial treatment.
- [85l9xy record](https://wallhaven.cc/api/v1/w/85l9xy) confirmed 4320x2700, sfw, 12 favourites. Its neon/grid/pillar/sphere tags also include `"name":"abstract"`; rejected conservatively against the user's generic-abstract exclusion.
- [mp5x7k record](https://wallhaven.cc/api/v1/w/mp5x7k) confirmed 1440x2560, sfw, 76 favourites. Tags include `"name":"kanji"`, `"name":"RGB"`, `"name":"chromatic aberration"`; insufficient evidence of a detailed scene, so rejected conservatively.
- eyx75o was rejected on hard fit: reported dimensions 4000x2759 fall below the minimum width/height ratio despite the API's rounded 1.45 label. Never accept a rounded ratio over exact dimensions.

### Mechs / hard sci-fi

Settled when two ultra, two laptop and two portrait armoured-machine scenes have individually checked metadata, or scarcity is adequately explained. **Open:** no retained picks. Digger results were anime/game character pieces rather than the requested machine scenes.

- The [mecha ultra search](https://wallhaven.cc/api/v1/search?q=mecha&purity=100&categories=111&sorting=toplist&topRange=1y&ratios=32x9&atleast=5120x1440) returned `"data":[]`, `"total":0`, quoted by the digger. This is query-limited absence, not proof no qualifying 32:9 mech wallpaper exists.
- [xepx7v](https://wallhaven.cc/api/v1/w/xepx7v) and [1qdj7g](https://wallhaven.cc/api/v1/w/1qdj7g) were individually read by diggers; both SFW, respectively 3840x2560 and 2160x3840. Both rejected for character-led subject matter. xepx7v was discovered by two agents and would have been deduplicated had it been retained.
- Digger reported inconsistent all-time versus annual query totals; query semantics were not resolved. Do not infer database-wide scarcity from that inconsistency. No size relaxation used and no weak substitutes inserted.

## Coverage

- Sci-fi concept art: partial — 2 ultra, 1 laptop; capital-ship and portrait frontier incomplete.
- Cyberpunk / neon cities: partial — 2 portrait; ultra/laptop city frontier incomplete.
- Real deep space: partial — 2 ultra with official-origin source fields; laptop/portrait provenance incomplete.
- Synthwave / outrun: partial — 1 portrait; remaining fits rejected or incomplete.
- Mechs / hard sci-fi: open — zero suitable machine scenes retained.
- Five diggers dispatched and five reports received. No missing report. No added sub-area.
- Digging ended: round 1 settled no complete sub-area and added no sub-area. Approximately 50 search/agent/record-read calls including the final verification pass; batching and failed ask steps consumed budget. No second round.

## Verification

12 compound source-record facts checked against 12 individual API pages: 289oex, 1q1oxg, e7dw1w, k7oy6m, ml2wvm, xej7ko, 2y5me6, 85l9xy, 7j91ee, zppjyg, mp5x7k, kw7ok7. Exact returned quotes confirm ID, resolution, SFW purity, favourites and direct path on every checked record. Tags were also returned for every checked record. Eight records retained; four excluded by conservative taste screening.

- NOT ON PAGE — detailed/cinematic/high-impact visual quality: metadata does not establish these subjective attributes; no such factual assurance is made for the picks.
- UNCHECKED — none of the 12 final record fetches failed. Independent pixel inspection, official-origin comparison and licensing review were not performed and are not claimed.
- Other digger findings with quoted record fields are cited above, but were not part of the 12-page final sample. Search absence is never generalized beyond the fetched query.

## Open rabbit holes

- dug, unsettled — High-favourite all-time rainy-neon / Blade Runner ultra and laptop searches; initial cyberpunk annual query was inadequate.
- dug, unsettled — Gundam, Titanfall and Warhammer machine-scene searches using reliable all-time favourites/tag variants; reported query-total inconsistency unresolved.
- dug, unsettled — Real-astronomy laptop and portrait provenance, including j3779m, r22ew1, 2klrm9 and mpexkk.
- dug, unsettled — Filtered outrun ultra/laptop searches and strong non-abstract synthwave scenes.
- undug — Direct capital-ship/battleship searches, GTGraphics catalogue, and artist-specific searches for Idil Dursun and Vadim Sadovski.
- undug — Visual inspection of retained images to determine whether they meet the user's exact taste; metadata alone cannot settle this.
- undug — Independent comparison with NASA/Webb originals and source licensing. Nothing downloaded or republished.
