I’ve found myself using this _a lot_. I noticed how much I was relying on it in October and wrote [Everything I built with Claude Artifacts this week](https://simonwillison.net/2024/Oct/21/claude-artifacts/), describing 14 little tools I had put together in a seven day period.

Since then, a whole bunch of other teams have built similar systems. GitHub announced their version of this—[GitHub Spark](https://simonwillison.net/2024/Oct/30/copilot-models/)—in October. Mistral Chat [added it as a feature called Canvas](https://mistral.ai/news/mistral-chat/) in November.

Steve Krouse from Val Town [built a version of it against Cerebras](https://simonwillison.net/2024/Oct/31/cerebras-coder/), showcasing how a 2,000 token/second LLM can iterate on an application with changes visible in less than a second.

Then in December, the Chatbot Arena team introduced [a whole new leaderboard](https://simonwillison.net/2024/Dec/16/webdev-arena/) for this feature, driven by users building the same interactive app twice with two different models and voting on the answer. Hard to come up with a more convincing argument that this feature is now a commodity that can be effectively implemented against all of the leading models.

I’ve been tinkering with a version of this myself for my Datasette project, with the goal of letting users use prompts to build and iterate on custom widgets and data visualizations against their own data. I also figured out a similar pattern for [writing one-shot Python programs, enabled by uv](https://simonwillison.net/2024/Dec/19/one-shot-python-tools/).

This prompt-driven custom interface feature is so powerful and easy to build (once you’ve figured out the gnarly details of browser sandboxing) that I expect it to show up as a feature in a wide range of products in 2025.

#### Universal access to the best models lasted for just a few short months

For a few short months this year all three of the best available models—GPT-4o, Claude 3.5 Sonnet and Gemini 1.5 Pro—were freely available to most of the world.

OpenAI made GPT-4o free for all users [in May](https://openai.com/index/hello-gpt-4o/), and Claude 3.5 Sonnet was freely available from [its launch in June](https://www.anthropic.com/news/claude-3-5-sonnet). This was a momentus change, because for the previous year free users had mostly been restricted to GPT-3.5 level models, meaning new users got a _very_ inaccurate mental model of what a capable LLM could actually do.

That era appears to have ended, likely permanently, with OpenAI’s launch of [ChatGPT Pro](https://openai.com/index/introducing-chatgpt-pro/). This $200/month subscription service is the only way to access their most capable model, o1 Pro.

Since the trick behind the o1 series (and the future models it will undoubtedly inspire) is to expend more compute time to get better results, I don’t think those days of free access to the best available models are likely to return.

#### “Agents” still haven’t really happened yet

I find the term “agents” extremely frustrating. It lacks a single, clear and widely understood meaning... but the people who use the term never seem to acknowledge that.

If you tell me that you are building “agents”, you’ve conveyed almost no information to me at all. Without reading your mind I have no way of telling which of the dozens of possible definitions you are talking about.

The two main categories I see are people who think AI agents are obviously things that go and act on your behalf—the travel agent model—and people who think in terms of LLMs that have been given access to tools which they can run in a loop as part of solving a problem. The term “autonomy” is often thrown into the mix too, again without including a clear definition.

(I also [collected 211 definitions](https://til.simonwillison.net/twitter/collecting-replies) on Twitter a few months ago—here they are [in Datasette Lite](https://lite.datasette.io/?json=https://gist.github.com/simonw/bdc7b894eedcfd54f0a2422ea8feaa80#/data/raw)—and had `gemini-exp-1206`[attempt to summarize them](https://gist.github.com/simonw/beaa5f90133b30724c5cc1c4008d0654).)

Whatever the term may mean, agents still have that feeling of perpetually “coming soon”.

Terminology aside, I remain skeptical as to their utility based, once again, on the challenge of **gullibility**. LLMs believe anything you tell them. Any systems that attempts to make meaningful decisions on your behalf will run into the same roadblock: how good is a travel agent, or a digital assistant, or even a research tool if it can’t distinguish truth from fiction?

Just the other day Google Search was caught [serving up an entirely fake description](https://simonwillison.net/2024/Dec/29/encanto-2/) of the non-existant movie “Encanto 2”. It turned out to be summarizing an imagined movie listing from [a fan fiction wiki](https://ideas.fandom.com/wiki/Encanto_2:_A_New_Generation).

[Prompt injection](https://simonwillison.net/series/prompt-injection/) is a natural consequence of this gulibility. I’ve seen precious little progress on tackling that problem in 2024, and we’ve been talking about it [since September 2022](https://simonwillison.net/2022/Sep/12/prompt-injection/).

I’m beginning to see the most popular idea of “agents” as dependent on AGI itself. A model that’s robust against gulliblity is a very tall order indeed.

#### Evals really matter

Anthropic’s [Amanda Askell](https://twitter.com/amandaaskell/status/1866207266761760812) (responsible for much of [the work behind Claude’s Character](https://simonwillison.net/2024/Jun/8/claudes-character/)):

> The boring yet crucial secret behind good system prompts is test-driven development. You don’t write down a system prompt and find ways to test it. You write down tests and find a system prompt that passes them.

It’s become abundantly clear over the course of 2024 that writing good automated evals for LLM-powered systems is **the skill** that’s most needed to build useful applications on top of these models. If you have a strong eval suite you can adopt new models faster, iterate better and build more reliable and useful product features than your competition.

Vercel’s [Malte Ubl](https://twitter.com/cramforce/status/1860436022347075667):
