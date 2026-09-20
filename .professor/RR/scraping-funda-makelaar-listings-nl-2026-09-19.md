# RR — Can a Dutch startup lawfully crawl funda.nl and makelaar sites for for-sale listings?

Question: Goal: verify FROM PRIMARY SOURCES the legal position of a Dutch startup that wants to collect for-sale home listings by crawling (a) funda.nl and (b) individual makelaar (estate agent) websites, so the founder can brief a Dutch IT lawyer. Return the saved report path first, then a cited answer. Claims 1-12 as listed (Innoweb; Ryanair; CV-Online; NVM v Zoekallehuizen; Databankenwet + spin-off; DSM arts 3-4 and Aw 15n/15o; AP scraping guidance; browsewrap; art. 138ab Sr; robots.txt/implied licence; Funda enforcement + VBO competition case; funda robots.txt). In scope: EU and Dutch law on listing data, 2006-2026. Not in scope: US law except one line on hiQ.

**This is legal research for a founder to brief a Dutch IT lawyer. It is not legal advice, and no lawyer-client relationship arises from it.**

---

## Bottom line

The two targets are legally very different: **individual makelaar websites** are close to the best case for a scraper — the leading Dutch authority (Hof Arnhem 2006, *NVM v Zoekallehuizen*) held that estate agents have **no database right** in their own listings, that copying address, asking price, a few lines of description and a thumbnail with a deep link was lawful, and that ignoring `robots.txt` is not unlawful. **funda.nl is the opposite**: it aggregates NVM's TIARA database (which the Hoge Raad's *El Cheapo* ruling left protectable), its terms expressly ban scraping and AI training, and — critically — it now serves an active human-verification bot wall on the very paths its `robots.txt` says are allowed. The biggest live risk is not copyright but **defeating that bot wall**, which turns a weak civil claim into possible art. 138ab Sr criminal exposure plus an art. 5a Databankenwet tort.

---

## Claim-by-claim

### 1. C-202/12 Innoweb v Wegener (2013) — CONFIRMED
**[FETCHED PRIMARY]** ECLI:EU:C:2013:850, 19 Dec 2013. A dedicated meta-search engine re-utilises the whole or a substantial part of a protected database, where it translates end-user queries "in real time" into the database's own search engine.

§47 — the operator "is not at all interested in the information stored in that database, but he provides the end user with a form of access to that database and to that information which is different from the access route intended by the database maker". §48 — it "comes close to the manufacture of a parasitical competing product". §46 — mere **consultation** of a database is NOT covered by the sui generis right.

**Limit the founder should note:** §24-25 expressly distinguish a dedicated meta-search engine from "a general search engine based on an algorithm, such as Google or Yahoo". Innoweb does not condemn crawling as such; it condemns *proxying another site's search function in real time*. A crawl-and-store architecture is governed by CV-Online (claim 3), not Innoweb.
[EUR-Lex 62012CJ0202](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX%3A62012CJ0202)

### 2. C-30/14 Ryanair v PR Aviation (2015) — CONFIRMED, with an important caveat
**[FETCHED PRIMARY]** 15 Jan 2015. Ruling, verbatim: Directive 96/9 "must be interpreted as meaning that it is not applicable to a database which is not protected either by copyright or by the sui generis right under that directive, so that Articles 6(1), 8 and 15 of that directive do not preclude the author of such a database from laying down contractual limitations on its use by third parties, **without prejudice to the applicable national law**."

**Caveat the claim as stated omits:** the CJEU decided only that EU database law does not *pre-empt* a contract. Whether a contract exists at all is left to national law — and §16 records that Ryanair's terms were **clickwrap**: the visitor "accepts the application of Ryanair's general terms and conditions **by ticking a box** to that effect." That distinction decided the Dutch sequel (claim 8), where Ryanair ultimately lost.
[EUR-Lex 62014CJ0030](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX%3A62014CJ0030)

### 3. C-762/19 CV-Online Latvia v Melons (2021) — CONFIRMED
**[FETCHED PRIMARY]** 3 June 2021. A search engine that **copies and indexes** a freely accessible database onto its own servers and lets users search it extracts and re-utilises the content — but the maker may prohibit this only "where those acts adversely affect its investment ... namely that they constitute a risk to the possibility of redeeming that investment through the normal operation of the database in question".

§44: "the main criterion for balancing the legitimate interests at stake must be the potential risk to the substantial investment of the maker of the database concerned, namely the risk that that investment may not be redeemed." §42, helpful to an aggregator: such services "contribute to the smooth functioning of competition and to the transparency of offers and prices."

This is the governing test for a crawl-and-store product, and it is **two-stage**: substantial investment first, then demonstrable harm to recoupment.
[EUR-Lex 62019CJ0762](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX%3A62019CJ0762)

### 4. Hof Arnhem 2006, NVM v Zoekallehuizen — CONFIRMED, and the Emerce premise CORRECTED
**[FETCHED PRIMARY]** **ECLI:NL:GHARN:2006:AY0089** (LJN AY0089), Gerechtshof Arnhem, **4 July 2006**, upholding Vzr. Rb. Arnhem 16 March 2006. Retrieved via `data.rechtspraak.nl`.

- **No database right** — r.o. 4.2: "het geheel van gegevens betreffende te koop aangeboden woningen niet kan worden beschouwd als een beschermde databank in de zin van artikel 1, lid 1 onder a, van de Databankenwet". r.o. 4.4: "de makelaars hebben ook in hoger beroep niet voldoende aannemelijk gemaakt dat zij in verband met het verkrijgen, controleren of presenteren van de gegevens op de website aanzienlijke investeringen hebben moeten doen **die zij anders niet zouden hebben gedaan**."
- **What was allowed** — r.o. 4.8: taking "adresgegevens, vraagprijs en enkele regels uit een meer omvangrijke beschrijving" and placing it alongside a hyperlink is lawful quotation under art. 15a Auteurswet.
- **Deep links** — r.o. 4.10: "het aanleggen van een deeplink geen openbaarmaking is van de webpagina waarnaar wordt gelinkt, ook niet als daartegen technische beschermingsmaatregelen zijn genomen."
- **Competition** — r.o. 4.18: NVM's advice to members to block ZAH "hebben de strekking om de mededinging op de landelijke woningmarkt ... in te perken, hetgeen in strijd is met het bepaalde in artikel 6 Mededingingswet."
- Dictum: "bekrachtigt het vonnis van de voorzieningenrechter in de rechtbank Arnhem van 16 maart 2006".
- *First-instance citation, probable:* **ECLI:NL:RBARN:2006:AV5236** (Rb. Arnhem, roll no. cited elsewhere as 136002 / KG ZA 06-25) appears in a practitioner survey as the makelaar-vs-Funda woninglijst case of that date. **Unverified** — not independently confirmed as the ZAH first-instance ruling.

**The Emerce headline: premise corrected.** "Gerechtshof: NVM misbruikt macht" is dated **5 July 2006**, not 2007, and reports **this very case** — the day after judgment. It is not a separate competition matter. [SECONDARY — [Emerce](https://www.emerce.nl/nieuws/gerechtshof-nvm-misbruikt-macht); the digger flagged its extracted quotes as lower-confidence, but date and subject are confirmed]

**A different item with a near-identical name:** "NVM misbruikt macht Funda", Het Financieele Dagblad, **31 July 2009**, by Joost Poort and Eske Scavenius — an **opinion column, not a ruling**. **[FETCHED PRIMARY text]** It says: "Er zijn diverse discussies en rechtszaken geweest over toegang van onafhankelijke partijen tot Funda en het recht van derden informatie van de site elders te publiceren. **Tot dusver trok Funda aan het langste eind: het weren van het aanbod van derden blijkt te mogen, zelfs als je marktmacht hebt.**" Do not cite as a court decision.

**Age caveat to carry to the lawyer:** ZAH partly rested on *geschriftenbescherming* (protection of non-original writings), **abolished with effect from 1 January 2015** (Wet 33800, deleting "alle" from art. 10(1)(1) Aw). That abolition makes the position for a scraper of plain factual listing data **better**, not worse. [SECONDARY — [Eerste Kamer 33800](https://www.eerstekamer.nl/wetsvoorstel/33800_afschaffing), [SOLV](https://solv.nl/blog/definitieve-einde-geschriftenbescherming/)]

### 5. Databankenwet, substantial investment and the "spin-off" doctrine — MIXED; Funda's position is far stronger than a makelaar's
**[FETCHED PRIMARY]** Databankenwet art. 1(1)(a) requires that "de verkrijging, de controle of de presentatie van de inhoud in kwalitatief of kwantitatief opzicht getuigt van een substantiële investering". Art. 2(1) grants the exclusive right over "opvragen" and "hergebruiken". [wetten.overheid.nl BWBR0010591](https://wetten.overheid.nl/BWBR0010591/)

**The spin-off doctrine at Dutch level — rejected. [FETCHED PRIMARY]** Hoge Raad 22 March 2002, **ECLI:NL:HR:2002:AD9138** (*NVM v De Telegraaf*, "El Cheapo"), r.o. 3.4.1: "Ook het door het Hof aan een uitlating van de Minister van Justitie bij de parlementaire behandeling van de Databankenwet ontleende **'spin-off'-argument mist in dit verband betekenis**, aangezien noch de Richtlijn noch de tekst van art. 1, onder a, Databankenwet een aanknopingspunt biedt voor de zienswijze dat, ingeval een databank voor meer doelen wordt gebruikt, voor elk van die doelen afzonderlijk een substantiële investering moet zijn aan te wijzen." The HR quashed the Hof Den Haag judgment and remanded to Hof Amsterdam. **The case then settled, so there is no final holding that NVM's database IS protected** — only removal of the spin-off obstacle. [SECONDARY on the settlement — AMI 2006/3 annotation, Alberdingk Thijm]

**But the EU-level limit is stricter and later.** CV-Online §25 restates the 2004 *British Horseracing Board*/*Fixtures Marketing* line: "investment in the obtaining of the contents of a database concerns the resources used to seek out existing independent materials and collect them in the database, **and not to the resources used for the creation as such of independent materials**." **[FETCHED PRIMARY]** That, not the Dutch spin-off label, is the operative constraint today — and it is exactly what defeated the makelaars in ZAH. A recent Dutch application of the same test is **ECLI:NL:RBMNE:2021:6183** (Rb. Midden-Nederland, KvK handelsregister: spin-off data, no substantial investment) — [SECONDARY ONLY, cited in an ICTRecht survey; not fetched], but it is a useful modern data point that Dutch courts still deny protection to by-product datasets.

**Who owns what. [FETCHED PRIMARY]** Rechtbank Amsterdam 25 Feb 2015, **ECLI:NL:RBAMS:2015:897**: NVM members must submit instructions to "een aan NVM toebehorend landelijk uitwisselingssysteem, genaamd **TIARA**", and "NVM heeft Funda bij oprichting een **exclusieve licentie voor onbepaalde tijd** verleend tot gebruik van de databanken van NVM." The database right, if any, sits with **NVM**; Funda is exclusive licensee.

*Naming discrepancy, unresolved:* the 2015 judgment names **TIARA**; the 2020 appeal judgment in the same litigation was extracted as naming **MIDAS**. Both from primary text. Unreconciled — the lawyer should check which system applies to which period.

### 6. TDM: DSM arts 3-4, Aw 15n/15o, Dbw 4a — does a ToS clause count as a reservation? THE DECISIVE ITEM
**[FETCHED PRIMARY]** DSM Directive 2019/790 art. 4(1) creates a TDM exception for anyone, commercial included, over "lawfully accessible" works. Art. 4(3): the exception applies "on condition that the use ... **has not been expressly reserved by their rightholders in an appropriate manner, such as machine-readable means in the case of content made publicly available online**." [EUR-Lex 32019L0790](https://eur-lex.europa.eu/legal-content/EN/TXT/HTML/?uri=CELEX:32019L0790)

**Recital 18, verbatim, is the crux and cuts both ways:** "In the case of content that has been made publicly available online, it should only be considered appropriate to reserve those rights by the use of machine-readable means, **including metadata and terms and conditions of a website or a service**." **[FETCHED PRIMARY]** The Directive's own recital *lists website terms among machine-readable means* — so "ToS clauses never count" overstates the law.

Dutch implementation **[FETCHED PRIMARY]**: **Auteurswet art. 15o(1)** — "Onverminderd het bepaalde in artikel 15n wordt een reproductie in het kader van tekst- en datamining niet als inbreuk op het auteursrecht ... beschouwd mits degene die de tekst- en datamining verricht **rechtmatig toegang** heeft tot het werk en het auteursrecht ... **niet uitdrukkelijk op passende wijze is voorbehouden, zoals door middel van machinaal leesbare middelen** bij een online ter beschikking gesteld werk." Art. 15n = research TDM. Database twin: **Databankenwet art. 4a(b)**, same wording; art. 4a(a) for research.

**The Dutch judgment on the point. [FETCHED PRIMARY, verbatim]** Rechtbank Amsterdam **30 October 2024, ECLI:NL:RBAMS:2024:6563** (DPG Media, Mediahuis, NRC v Knowledge Exchange BV h/o HowardsHome, C/13/737170 / HA ZA 23-690):
- r.o. 4.32: because the publishers made the material publicly and freely accessible, "heeft HowardsHome in beginsel het recht om die openbare teksten te lezen en op geautomatiseerde wijze te analyseren. HowardsHome heeft daar in beginsel dus **rechtmatig toegang** toe."
- r.o. 4.33: "De Uitgevers hebben tegenover de gemotiveerde betwisting door HowardsHome **onvoldoende onderbouwd** dat de tekst- of datamining van de websites in machinaal leesbare middelen uitdrukkelijk door de Uitgevers is voorbehouden. ... HowardsHome heeft aangevoerd dat daarmee alleen bepaalde AI-bots worden uitgesloten zoals GPTBot, ChatGPT-User, CCBOT en anthropie-ai. ... Daarmee staat onvoldoende vast dat het auteursrecht op de websites van de Uitgevers in machinaal leesbare middelen op passende wijze is voorbehouden. **HowardsHome wordt daarom gevolgd in haar stelling dat haar een beroep toekomt op artikel 15o Aw.**"
- r.o. 4.43: the same reasoning defeats the **database-right** claim, "omdat de toets van artikel 4a Dbw gelijk is".

**Read it precisely.** The court did **not** hold that robots.txt is the only valid vehicle, nor that ToS prose can never count. It held that *on this record* the publishers failed to discharge their burden of proving a machine-readable reservation covering this defendant's crawler. A Dutch commentator ([ie-forum case tracker](https://www.ie-forum.nl/artikelen/case-tracker-ai-en-auteursrecht-2026-europa-amerika), [SECONDARY]) glosses it more categorically — "only a specific and technically recognisable reservation in robots.txt suffices". That gloss is stronger than the ratio; do not rely on it.

**German comparison** [SECONDARY ONLY — [Norton Rose Fulbright](https://www.insidetechlaw.com/blog/2025/12/machine-readable-opt-outs-and-ai-training-hamburg-court-clarifies-copyright-exceptions)]: OLG Hamburg, *Kneschke v LAION*, 5 U 104/24, 10 Dec 2025, reportedly held "general terms of use or human-readable disclaimers are insufficient", naming robots.txt, the TDM Reservation Protocol and metadata as examples; appeal to the BGH was allowed. The first-instance LG Hamburg judgment (310 O 227/23, 27 Sept 2024) is reported to have been more open to natural-language reservations "depending on the state of the art". **Neither German judgment could be fetched** — the contrast is secondary-only and unresolved.

**Applied to Funda.** Funda's terms **[FETCHED PRIMARY]**: "Het is je niet toegestaan om zonder voorafgaande schriftelijke toestemming van Funda, het platform of het daarop beschikbaar gestelde materiaal te verveelvoudigen, te kopiëren, te wijzigen, te distribueren, te verspreiden, te 'reverse engineeren', **te 'scrapen'**, te decompileren, te framen, **'te dataminen'**, beschikbaar te stellen, **te gebruiken voor het trainen van (generatieve) kunstmatige intelligentie** of op andere wijze te gebruiken en/of te exploiteren." But Funda's `robots.txt` contains **no TDM or AI-crawler directive whatsoever**. On the *HowardsHome* reasoning that is a real weakness in any Funda reservation argument — though recital 18's "terms and conditions of a website" language makes it genuinely contestable, not settled.

### 7. AP on scraping; is a home address personal data? — CONFIRMED, with a correction
**Correction to the premise:** the document is the **"Handreiking scraping door particulieren en private organisaties"**, not a "Standpunt". The obtainable version is the **April 2025 revision**; the original appeared around 1 May 2024. **[FETCHED PRIMARY — April 2025 PDF]** [AP landing page](https://www.autoriteitpersoonsgegevens.nl/documenten/handreiking-scraping-door-particulieren-en-private-organisaties)

- Headline, p.3: "Als bij scraping (ook) persoonsgegevens worden verzameld – wat al snel het geval zal zijn – is vrijwel altijd de Algemene verordening gegevensbescherming (AVG) van toepassing... Deze omstandigheden maken het **moeilijk om scraping rechtmatig** (volgens de wet) in te zetten."
- Public data is no free pass, p.3/p.11: "Een grondslag heeft u **óók** nodig als u alleen informatie verzamelt die rechtmatig op internet is geplaatst en die voor iedereen openbaar toegankelijk is."
- p.11: "Voor u als private organisatie of particulier zal de grondslag 'gerechtvaardigd belang' doorgaans de **enige** grondslag zijn die in aanmerking komt."
- **The KNLTB effect is real and the AP has absorbed it**, p.12: "Om te kunnen spreken van een gerechtvaardigd belang, is het noodzakelijk dat het aangevoerde belang rechtmatig is. Het is hiervoor **niet vereist dat een belang is vastgelegd in een wet**." — footnotes citing **CJEU C-621/22 (KNLTB), 4 Oct 2024**, §40 and §49.
- CJEU C-621/22 **[FETCHED PRIMARY]**: §16 records the AP's position that legitimate interests "are only interests that are enshrined in and determined by law"; §39-40 and §48-49 reject it — a commercial interest "could constitute a legitimate interest ... provided that it is not contrary to the law." [EUR-Lex 62022CJ0621](https://eur-lex.europa.eu/legal-content/EN/TXT/?uri=CELEX%3A62022CJ0621)

**Is an address personal data? Yes, on the AP's own statement. [FETCHED PRIMARY]** AP, "Wat zijn persoonsgegevens?": "Voor de hand liggende persoonsgegevens zijn: iemands naam; **adres**; telefoonnummer; pasfoto." Not personal data: "gegevens over organisaties (rechtspersonen); gegevens van overleden personen."
AP, "Persoonsgegevens in openbare bronnen": "Het feit dat persoonsgegevens te vinden zijn in open bronnen, of daar zelfs door de betrokkene zelf zijn geplaatst, is dus **geen vrijbrief** om de gegevens opnieuw te verwerken."

*Practical nuance the AP does not address:* a typical listing identifies the **property and the makelaar**, not the seller by name. Whether a given address is personal data turns on identifiability of the natural person living there — usually satisfiable by combination. No AP text found on the sale-listing fact pattern specifically; a genuine gap.

*Provenance caveat:* those two AP web pages reached me through a harvester cross-wiring incident (they were another agent's requests). Content is coherent and matches the AP's site structure, and that agent independently requested exactly those URLs — but re-open both directly before quoting.

### 8. Are website terms binding on a visitor who never clicks accept? — NO, on the best Dutch authority
**[FETCHED PRIMARY]** The Dutch sequel to Ryanair, directly on point.
- **Hoge Raad 11 March 2016, ECLI:NL:HR:2016:390** — applied C-30/14, quashed the Hof Amsterdam, remanded to **Gerechtshof Den Haag** precisely to decide whether PR Aviation had *accepted* the terms.
- **Gerechtshof Den Haag 23 January 2018, ECLI:NL:GHDHA:2018:61** (zaaknr. 200.189.603/01) — the substantive holding. Ryanair used pure **browse-wrapping**, distinguished from click-wrapping (r.o. 32-41). r.o. 78: "de vraag [is] of, objectief gezien, een redelijk persoon denkt dat PR Aviation de (rechtskeuze in de) gebruiksvoorwaarden wilde aanvaarden door gebruik te maken van de website van Ryanair (vgl. naar Nederlands recht **art. 3:35 BW**)." r.o. 79, the wall-poster analogy: "wie op straat aan een muur ... een aanplakbiljet heeft opgehangen met een tekst waarvan de eerste regel luidt: 'Wie verder leest, moet € 5,- betalen', mag er niet zo maar op vertrouwen dat een voorbijganger die de tekst verder leest, zich heeft willen binden aan deze voorwaarde." r.o. 81, what *would* bind: "Daarvoor is bijvoorbeeld vereist dat de gegevens **door een technische maatregel niet vrij toegankelijk zijn**, maar pas worden vrijgegeven als de bezoeker van de website **expliciet akkoord gaat** met de gebruiksvoorwaarden." r.o. 96, expressly on Dutch law: "Het hof merkt op dat dit onder Nederlands recht niet anders zou zijn ... Ryanair mocht er ... redelijkerwijs niet op vertrouwen dat PR Aviation, door de website van Ryanair te bezoeken en aldaar gegevens te verzamelen, gebruiksvoorwaarden ... wilde aanvaarden (art. 3:35 BW)."
- **Hoge Raad 27 September 2019, ECLI:NL:HR:2019:1445** — cassation dismissed under **art. 81(1) RO**, so the Hof Den Haag holding stands. The HR frames the question as: "Heeft gebruiker de gebruiksvoorwaarden van de site aanvaard door 'browse wrapping' (louter surfen na mededeling op homepage dat deze voorwaarden van toepassing zijn, met link naar gebruiksvoorwaarden)?"

**Two caveats.** Because the HR disposed of it under art. 81 RO, it wrote no reasoning of its own — the authority is a *gerechtshof* judgment the HR let stand. And r.o. 81 is the warning: **Funda's bot wall is arguably exactly the "technische maatregel" that moves this from browsewrap toward gated assent.**

### 9. Could bypassing a CAPTCHA or bot wall be computervredebreuk (art. 138ab Sr)? — UNRESOLVED, and the live risk
**[FETCHED PRIMARY — wetten.overheid.nl]** Art. 138ab(1) Sr: "Met gevangenisstraf van ten hoogste twee jaren of geldboete van de vierde categorie wordt, als schuldig aan computervredebreuk, gestraft hij die **opzettelijk en wederrechtelijk binnendringt in een geautomatiseerd werk** of in een deel daarvan. Van binnendringen is in ieder geval sprake indien de toegang tot het werk wordt verworven: a. door het **doorbreken van een beveiliging**, b. door een **technische ingreep**, c. met behulp van **valse signalen of een valse sleutel**, of d. door het **aannemen van een valse hoedanigheid**." Lid 2 (copying data found inside) and lid 3 raise the maximum to four years.

**No Dutch authority found applying 138ab to CAPTCHA/bot-wall/rate-limiter bypass.** Be precise about the strength of that statement: two multi-term searches returned nothing on point, and a Dutch practitioner survey of scraping law ([ICTRecht](https://www.ictrecht.nl/blog/hier-met-die-data-hoe-zit-dat-nou-precies-met-scraping)) does not raise 138ab at all — but **no dedicated search string for the CAPTCHA/Cloudflare/user-agent fact pattern was ever run**, so the search space is not exhausted. This is "not found", weaker than "does not exist".

**The closest primary case, and it is only adjacent. [FETCHED PRIMARY]** Hoge Raad **19 March 2024, ECLI:NL:HR:2024:455** (strafkamer, 22/03276): the Hof had **acquitted** a defendant who allegedly SQL-injected a GP practice website, reasoning that the website "als zodanig" is not an "inrichting" and so not a "geautomatiseerd werk" as charged. The HR rejected the prosecution's cassation appeal, r.o. 2.6.2: the Hof's reading of the charge "is ... niet onverenigbaar met de bewoordingen van de tenlastelegging en moet in cassatie worden geëerbiedigd." **This is a narrow ruling about how that particular charge was drafted, not a general holding that a website cannot be "binnendringen".** Do not over-read it.

Dutch commentary leans the scraper's way for *ordinary* scraping of public pages: scraping requests data "public and deliberately made accessible", so no prohibited area is entered [SECONDARY ONLY — [Ius Mentis](https://blog.iusmentis.com/2016/11/04/is-scrapen-website-computervredebreuk/)]. Commentary also places **IP-spoofing** under "valse sleutel" rather than "valse hoedanigheid", and notes the four categories are illustrative, not exhaustive. The specifically-requested scholarly treatment (Koops & Oerlemans, "Materieel strafrecht en ICT") **could not be retrieved** — see the failure table.

**Civil hook — Databankenwet art. 5a(1) [FETCHED PRIMARY]**: "Degene, die doeltreffende technische voorzieningen omzeilt en dat weet of redelijkerwijs behoort te weten, **handelt onrechtmatig**." Art. 1(1)(e) makes measures "doeltreffend" only where access is "beheerst door middel van toegangscontrole of door toepassing van een beschermingsprocédé zoals encryptie, vervorming of andere transformatie ... of een kopieerbeveiliging die de beoogde bescherming bereikt."
**No Dutch case post-2006 was found applying art. 5a Dbw to an anti-scraping measure** — corroborated independently by two sources. So whether a modern bot wall is "doeltreffend" is legally untested.

Binding authority that favours the scraper on 2006 facts — **Hof Arnhem, ZAH, r.o. 4.11 [FETCHED PRIMARY]**: "Technische voorzieningen worden pas geacht doeltreffend te zijn indien het opvragen en hergebruiken van een databank ... wordt beheerst door middel van toegangscontrole of door middel van een beschermingsprocédé of een kopieerbeveiliging die de beoogde bescherming bereikt. ... **Voorzieningen die uitsluitend gericht zijn tegen ZAH als zodanig vallen niet onder de wettelijke bescherming en het ontgaan daarvan levert niet onrechtmatig handelen van ZAH op.**" And: "In het bijzonder het gebruik maken van **voor de makelaars niet herkenbare IP-adressen** kan niet als een verboden ontduiking van de beschermings- of beveiligingsmaatregelen worden beschouwd."

**The decisive factual change since 2006 — verified first-hand.** ZAH's measures were ad-hoc and aimed at one actor. Funda's are not. On 19 Sept 2026 I fetched three funda.nl paths and every one returned a human-verification interstitial instead of content: `/zoeken/koop/`, `/gebruiksvoorwaarden/`, `/voorwaarden-en-beleid/aansluitvoorwaarden/funda/`. Verbatim: **"Je bent bijna op de pagina die je zoekt"** / **"We houden ons platform graag veilig en spamvrij. Daarom moeten we soms verifiëren dat onze bezoekers echte mensen zijn."** A generally-applied verification gate is a far better candidate for "toegangscontrole" than anything in ZAH. (Vendors assert Funda runs **Akamai Bot Manager** — *unverified*, search-snippet only, from parties with a commercial interest.)

**Manner-of-scraping tort (art. 6:162 BW).** The theory has been pleaded in a Dutch real-estate scraping case and failed **on the facts, not the law**: *PropertyNL v RealNext*, Vzr. Rb. Amsterdam 26 April 2016, **ECLI:NL:RBAMS:2016:2603** — PropertyNL produced screenshots of rapid automated requests, but "PropertyNL [heeft] onvoldoende aannemelijk kunnen maken dat RealNext zich schuldig maakt aan het haar verweten gedrag". No Dutch case was found applying "profiteren van andermans prestatie" to scraping. [SECONDARY — [itenrecht.nl](https://www.itenrecht.nl/artikelen/geen-sprake-van-schaduwplatform-of-scrapen-van-databankenrecht-aanbodgegevens-vastgoed)] So server-load/hindrance liability is an open, untested theory — not a safe harbour.

### 10. robots.txt and "if Google may index it, anyone may crawl it" — DIRECT DUTCH AUTHORITY, against the site owner
**[FETCHED PRIMARY]** *The best answer in the report, squarely on the founder's facts.* Hof Arnhem, ZAH, **r.o. 4.11**: "Ook indien juist is dat andere zoekmachines maatregelen als robot.txt (**die niet verhinderen doch slechts verzoeken**) respecteren, volgt daaruit niet dat ZAH door het enkele feit dat zij zich niet aan deze **'code' of 'etiquette'** houdt, onrechtmatig jegens de makelaars handelt."

Under Dutch law, **ignoring robots.txt is not by itself an unlawful act.** But note the asymmetry: robots.txt has no *prohibitive* force, yet under DSM art. 4(3) it is the leading candidate for the *rightholder's* machine-readable reservation, where its presence hurts the scraper (claim 6). Ignoring a `Disallow` is not a tort; ignoring a machine-readable TDM reservation forfeits the art. 15o defence.

**On the implied-licence argument ("Google may, so anyone may"): no EU or Dutch authority found, for or against.** Searched and not found. Innoweb §24-25, distinguishing general algorithmic search engines from dedicated meta-engines, is the closest thing and points the other way — the CJEU treats Google's position as *distinguishable*, not as a benchmark others may claim.

**US contrast, one line as scoped:** *hiQ Labs v LinkedIn*, 938 F.3d 985 (9th Cir. 2019), reaffirmed 31 F.4th 1180 (9th Cir. 2022), held scraping public data does not violate the CFAA's "exceeds authorized access" clause — but in Nov 2022 the district court found hiQ had **breached LinkedIn's User Agreement** and the parties settled; so even the leading pro-scraper US case ended on contract, which is the Dutch fault line too. [SECONDARY — [Wikipedia](https://en.wikipedia.org/wiki/HiQ_Labs_v._LinkedIn)]

### 11. Funda's enforcement history, and is Funda obliged to give access? — NO obligation; NO enforcement found
**Enforcement: searched, no reported action found.** No lawsuit, injunction, cease-and-desist or public statement by Funda against a scraper, aggregator or extension was located. Two adjacent Dutch real-estate scraping cases exist but **do not involve Funda as a party**: *PropertyNL v RealNext* (ECLI:NL:RBAMS:2016:2603, above) and the Gaspedaal/Autotrack matter, which is the *Innoweb* car-listings case, not housing.
Circumstantially consistent with non-enforcement: a visible commercial market of Funda scraping APIs and open-source scrapers operates publicly (ScrapingBee, ZenRows, Apify, scrape.do, `pyfunda`, GitHub projects) with no located takedown. Absence of evidence, not evidence of tolerance. Funda also has **no public API**; access is by partner licence only.

**The competition litigation: Funda WON, and owes no access. [FETCHED PRIMARY]** Gerechtshof Amsterdam **26 May 2020** (date confirmed against Jure.nl, rechtspraak.nl and InView; several secondary sources misreport it as 17 or 18 June 2020), **ECLI:NL:GHAMS:2020:1337**, zaaknrs. 200.243.424/01 and 200.243.436/01, VBO Makelaar v Funda. r.o. 3.6: the court **assumed arguendo** Funda holds a dominant position. r.o. 3.27.1: "Vooropgesteld dient te worden dat het ook een onderneming met een machtspositie **in beginsel vrijstaat om te kiezen met wie en op welke voorwaarden zij wenst te contracteren**." Access would require the database be "onontbeerlijk ... zodat de database voor die markt als een **essential facility** moet worden beschouwd" — VBO failed. Preceded by Rb. Amsterdam 25 Feb 2015 (ECLI:NL:RBAMS:2015:897, interlocutory) and VBO's 2018 first-instance loss (reported as ECLI:NL:RBAMS:2018:1654) [SECONDARY — [Maverick](https://www.maverick-law.com/en/blogs/insufficient-evidence-of-abuse-of-dominant-position-by-funda-property-website.html), [Mr. Online](https://www.mr-online.nl/onvoldoende-bewijs-misbruik-machtspositie-door-woningsite-funda/)].

**Answer to the founder's actual question: no, Funda is under no legal duty to give anyone access to its listings.** Note the historical irony that in 2006 NVM's *blocking* conduct was itself held contrary to art. 6 Mw (ZAH r.o. 4.18) — a coordinated-boycott theory, quite different from the unilateral-refusal theory VBO lost on.

*Unverified lead:* one search summary asserted "the NMa had earlier stated that all brokers should receive equal treatment on Funda". No NMa/ACM decision, case number or date was located, and the Mr. Online piece contains **no** NMa/ACM reference. Treat as unverified.

### 12. funda.nl/robots.txt — FETCHED, and it is permissive
**[FETCHED PRIMARY, 19 Sept 2026]** [https://www.funda.nl/robots.txt](https://www.funda.nl/robots.txt). Complete content: a single `User-agent: *` group. **Allow:** `/zoeken/`, `/detail/`, `/informatie/`, `/mijn-huis/`, `/favorieten/`, `/hypotheek/maandlasten/ing/$`, `/makelaar-zoeken/`, `/meer-weten/`, `/voormakelaars/`. **Disallow:** `/account/` ("Prevent bots from indexing account pages"), `/zoeken/koop/*,*` and `/zoeken/huur/*,*` ("Prevent bots from indexing combinations of locations"). Sitemap: `https://www.funda.nl/sitemap_index.xml/`.

**No AI-crawler lines at all** — no GPTBot, CCBot, anthropic-ai, Google-Extended, ClaudeBot; no `Crawl-delay`; no TDM reservation of any kind. Listing detail pages (`/detail/`) and search (`/zoeken/`) are expressly **allowed**.

**The contradiction that matters:** `robots.txt` says `Allow: /zoeken/`, yet `/zoeken/koop/` served me a human-verification wall (claim 9). Funda's machine-readable signal permits what its infrastructure blocks. That asymmetry is genuinely useful to a lawyer: it weakens Funda's art. 4(3) reservation (claim 6) while strengthening its "doeltreffende technische voorziening" position (claim 9).

---

## What this adds up to, for the lawyer briefing

1. **Makelaar sites** — strongest position. ZAH is directly on point and still good law on database right, deep-linking and robots.txt; the *geschriftenbescherming* leg it partly rested on has since been abolished, which helps. Residual risks: art. 15a Aw quotation limits if you take more than address/price/a few lines/thumbnail; photos carry their own copyright; onrechtmatige daad if the crawl burdens their servers (untested, see claim 9).
2. **funda.nl** — weakest position, and the exposure is *not* mainly copyright. It is (a) the bot wall, art. 138ab Sr and art. 5a Dbw; (b) NVM's TIARA database right, which *El Cheapo* left viable and which is a far better candidate for substantial investment than any single makelaar's site; and (c) contract — where *Hof Den Haag 2018* says you are probably not bound by unclicked terms, but r.o. 81 warns a technical gate changes the analysis.
3. **TDM/AI training** — art. 15o Aw and art. 4a(b) Dbw are real defences, and *HowardsHome* shows a Dutch court applying them against publishers who could not prove a machine-readable reservation. Funda's robots.txt reserves nothing. But recital 18 names website T&Cs as a machine-readable means, so the point is contestable, and the German OLG Hamburg line (secondary only) is less generous.
4. **GDPR runs in parallel and is indifferent to all of the above.** The AP's Handreiking says you need a ground even for lawfully-published public data; legitimate interest is the only realistic one; post-KNLTB a commercial interest can qualify if lawful. Address is personal data on the AP's own definition.

---

## Open questions after two rounds

- **No Dutch authority on whether defeating a CAPTCHA/bot wall is "binnendringen" under 138ab Sr** — and the search space was not exhausted, so this is "not found", not "does not exist". The most consequential unknown for the Funda plan.
- **Whether a publicly accessible site can be "binnendringen" at all** without a breached security measure — statute fetched; HR:2024:455 is adjacent and narrow; the leading scholarly chapter could not be retrieved.
- **No Dutch case post-2006 applying art. 5a Dbw to modern anti-bot measures** — corroborated by two sources; whether an Akamai-style wall is "doeltreffend" is untested.
- **Manner-of-scraping tort** — pleaded and lost on the facts in PropertyNL v RealNext; the legal standard remains unset.
- **TIARA vs MIDAS** — two primary judgments in one litigation name different systems; unreconciled.
- **The original May 2024 AP Handreiking wording** — only the April 2025 revision was obtainable.
- **The two German LAION judgments** — secondary-only; the LG/OLG tension is unresolved.
- **Any NMa/ACM decision on NVM/Funda access, or a recent ACM housing-platform study** — not located.
- **Whether funda.nl's robots.txt ever carried AI-crawler directives** — historical snapshot not retrievable.
- **Whether Funda has ever sent an unreported cease-and-desist** — only litigated matters are visible.

---

## Sources we could not get

| Source / URL | Tool + error | What stays unverified |
|---|---|---|
| `curia.europa.eu/juris/liste.jsf?num=C-202/12` | harvester: "Retrieval failed. Retry or choose another work." | Nothing — Innoweb obtained in full from EUR-Lex. |
| `wetten.overheid.nl/BWBR0001886/` (Auteurswet) | harvester: "could not read or publish its stored result" | Nothing — obtained via the `/2024-01-01` variant. |
| `wetten.overheid.nl/jci1.3:c:BWBR0001886&artikel=15o` | harvester **mis-served**: returned a 2009 FD newspaper article | Art. 15o — later obtained from the full Auteurswet text. (The mis-served article proved independently useful.) |
| `data.rechtspraak.nl/...ECLI:NL:GHAMS:2020:1337` | harvester **mis-served**: returned an AP web page | Nothing — recovered via WebFetch. |
| `data.rechtspraak.nl/...ECLI:NL:RBAMS:2015:897` | harvester **mis-served**: returned an AP web page | Nothing — recovered via WebFetch. |
| `wetten.overheid.nl/jci1.3:c:BWBR0001854&...artikel=138ab` | WebFetch: table of contents only, article body absent | Art. 138ab — later obtained by a digger via harvester. |
| `wetten.overheid.nl/BWBR0001854/2024-07-01/0/...Artikel138ab` | WebFetch: TOC only | as above |
| `maxius.nl/wetboek-van-strafrecht/artikel138ab` | WebFetch: site database error (Foutcode 3 / 1030) | as above |
| `nl.wikipedia.org/wiki/Computervredebreuk` | fetched, but paraphrases rather than reproduces the statute | used only as cross-check |
| Koops & Oerlemans, "Materieel strafrecht en ICT" (Leiden repository) | WebFetch: **`read ECONNRESET`**, failed twice including a deliberate retry | The specifically-requested scholarly treatment of "binnendringen" on unsecured public sites. ResearchGate mirror not attempted. |
| `magazines.openbaarministerie.nl/opportuun/2024/03/jurisprudentie` | **`getaddrinfo ENOTFOUND`** (DNS failure) | OM commentary on the website-as-geautomatiseerd-werk line. |
| `bijzonderstrafrecht.nl` piece on "valse sleutel" / authorised login | not attempted (budget) | Whether technically-permitted-but-substantively-forbidden access is a "valse sleutel". |
| `boek9.nl/.../B9 IEPT20241030 II, Rb Amsterdam, Uitgevers tegen HowardsHome.pdf` | harvester: "could not read or publish its stored result" | Nothing — r.o. 4.31/4.32/4.33/4.43 obtained verbatim from `data.rechtspraak.nl`. |
| `twobirds.com/.../kneschke-v-laion` | WebFetch: **HTTP 402 Payment Required** | LG Hamburg's first-instance reasoning on natural-language TDM reservations. |
| LG Hamburg 310 O 227/23; OLG Hamburg 5 U 104/24 | full texts never located | Both LAION holdings — **secondary only**. |
| `uitspraken.rechtspraak.nl/inziendocument?id=ECLI:NL:GHARN:2006:AY0089` | WebFetch: JS shell only | Nothing — recovered via `data.rechtspraak.nl`. |
| `jure.nl/AY0089` | HTTP 503 | as above |
| `solv.nl/files/publications/110Noot_ZAH.pdf` | harvester: "Retrieval failed" | The AMI 2006/3 annotation; its quotes are **secondary only**. |
| `ivir.nl/publicaties/download/07 - nvm-misbruikt-macht-funda.pdf` | WebFetch: metadata only, no body | Nothing — the FD article body was obtained via the mis-served fetch above. |
| `web.archive.org/web/2025/https://www.funda.nl/robots.txt` (+ `if_` variant) | harvester returned Wayback chrome, not the file body | Whether funda.nl's robots.txt ever contained AI-crawler directives. |
| `funda.nl/gebruiksvoorwaarden/`; `funda.nl/zoeken/koop/`; `funda.nl/voorwaarden-en-beleid/aansluitvoorwaarden/funda/` | WebFetch: **bot-verification interstitial returned instead of content** | Funda's Aansluitvoorwaarden (supplier terms) — never read. *These failures are themselves reported as a finding under claim 9.* The Gebruiksvoorwaarden text quoted above comes from a cached 12 March 2026 archive snapshot, not the live page. |
| `browserless.io/skills/funda.nl` | fetched, contained nothing about funda.nl | The Akamai Bot Manager attribution stays **unverified**. |
| ECLI:NL:RBAMS:2018:1654 (VBO v Funda, first instance) | not fetched | Its reasoning — **secondary only**. |
| ECLI:NL:RBAMS:2016:2603 (PropertyNL v RealNext) | not fetched | Its reasoning — **secondary only**. |
| ECLI:NL:RBMNE:2021:6183 (KvK handelsregister, spin-off) | not fetched | Its reasoning — **secondary only**. |
| ECLI:NL:RBARN:2006:AV5236 (probable ZAH first instance) | not fetched | Whether this is in fact the Zoekallehuizen first-instance ruling. |
| Emerce, "Gerechtshof: NVM misbruikt macht" | fetched by a digger; quote precision flagged low-confidence | Exact wording; **date (5 July 2006) and subject are confirmed**. |
| AP Handreiking scraping, **original May 2024** edition | not located; only April 2025 revision obtained | What the AP said about purely commercial interests pre-KNLTB. |
| AP decision, Locatefamily.com €525,000 fine | not fetched | AP reasoning on address data in an enforcement setting. |
| NMa/ACM decisions on NVM/Funda; ACM housing-platform market study 2020-2026 | not located | Whether any administrative competition track exists alongside the civil VBO case. |

**Dispatch reconciliation: 6 diggers dispatched (4 in round 1, 2 in round 2); 6 reports received.** Round 1's four were re-dispatched as `general-purpose` after the `Explore` agent type was refused by a policy hook. The final digger (art. 138ab case law, art. 5a Dbw, manner-of-scraping tort) returned after the first version of this report was written; its findings are integrated into claims 4, 5 and 9 above.
