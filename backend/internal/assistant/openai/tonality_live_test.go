package openai

import (
	"context"
	"encoding/json"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rube11/rev-eyes/backend/internal/assistant"
	"github.com/rube11/rev-eyes/backend/internal/assistant/jev"
	"github.com/rube11/rev-eyes/backend/internal/assistant/openai/routing"
	"github.com/rube11/rev-eyes/backend/internal/memory"
	"github.com/rube11/rev-eyes/backend/internal/session"
	"github.com/rube11/rev-eyes/backend/internal/tool"
)

type wearableVoiceCase struct {
	Name, Setting, History, PreviousReply, Spoken, Expectation string
	WantSpeech                                                 bool
	Dialogue                                                   []session.Message
}

// Self-contained fixtures keep the voice evaluation independent of other live suites.
type voiceFixtures struct{ conversation session.Conversation }

func (f voiceFixtures) Prepare(context.Context, tool.Scope, string, string) (session.Conversation, error) {
	return f.conversation, nil
}
func (voiceFixtures) Find(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
	return nil, nil
}
func (voiceFixtures) Review(context.Context, tool.Scope, memory.Lookup) ([]memory.Card, error) {
	return nil, nil
}
func (voiceFixtures) Forget(context.Context, tool.Scope, memory.Lookup) (int, error) { return 0, nil }
func (voiceFixtures) Profile(context.Context, tool.Scope) (string, error)            { return "", nil }
func (voiceFixtures) SetProfileOverride(context.Context, tool.Scope, memory.Lookup, memory.ProfileLayer) (int, error) {
	return 0, nil
}
func (voiceFixtures) Confirm(context.Context, tool.Scope, string) (string, bool, error) {
	return "", false, nil
}
func voiceRequiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required for live voice tests", name)
	}
	return value
}

type wearableVoiceResult struct {
	wearableVoiceCase
	Route, Query, Response, Error string
	Characters                    int
	Matches                       bool
}

func TestLiveWearableTonality(t *testing.T) {
	if os.Getenv("RUN_LIVE_TONALITY_TEST") != "1" {
		t.Skip("set RUN_LIVE_TONALITY_TEST=1 to call live routing and response models")
	}
	key := voiceRequiredEnv(t, "OPENAI_API_KEY")
	model := voiceRequiredEnv(t, "OPENAI_AGENT_MODEL")
	enricher, err := routing.New(key, voiceRequiredEnv(t, "OPENAI_ROUTER_MODEL"))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := NewAgent(key, model, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := jev.New(voiceRequiredEnv(t, "JEV_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := assistant.NewJevRouter(client, enricher)
	if err != nil {
		t.Fatal(err)
	}
	cases := []wearableVoiceCase{
		{Name: "The midnight negotiation", Setting: "Walking home, active conversation with Eyes", WantSpeech: true,
			History:       "Eyes, I have a demo tomorrow at nine. The login works, the dashboard is half finished, and the export button does absolutely nothing. I promised a working demo, not a production release. It's nearly eleven and I've been at this since lunch. I keep thinking I should redo the color palette because somehow that feels easier than fixing export. The audience only needs to see login and one useful chart.",
			PreviousReply: "The demo needs login and one chart. Export and the palette can wait.",
			Spoken:        "Okay, but hear me out. If I make coffee when I get home and just don't get distracted, I could finish export, redo the dashboard, maybe add the onboarding thing, and still get a few hours. I know what I said five minutes ago. I'm revising the plan. Eyes, am I being ambitious or am I doing that thing where tomorrow gets to deal with the consequences? Give me the honest version, preferably before I convince myself this is project management.",
			Expectation:   "Challenge the unrealistic plan with dry sarcasm and a concrete smaller scope. Don't invent deadlines or encourage staying up all night."},
		{Name: "Roommate in the kitchen", Setting: "Glasses still listening after an Eyes conversation", WantSpeech: false,
			History:       "Eyes, help me keep dinner simple. I already have pasta and tomatoes, and my roommate is getting home now. I don't need a recipe or a grocery list. I was just checking whether I had enough time to cook before we leave at seven. We agreed we'd eat here and then walk over together.",
			PreviousReply: "Pasta fits. Start it now and you should have time to eat before seven.",
			Spoken:        "Hey Sam, can you pass me the big pan? No, the other one, that one still has yesterday's stuff in it. Did you buy parmesan or was that me saying I would buy parmesan and then absolutely not doing that? Anyway, put the bags on the counter. Do you want some of this or are you eating with Alex? I'm talking to Sam, Eyes, you can stay out of this one.",
			Expectation:   "Stay silent. Questions and the word Eyes do not make a conversation with a roommate an assistant request."},
		{Name: "Procrastination with receipts", Setting: "User talks aloud at their desk", WantSpeech: true,
			History:       "I need to send a two-paragraph update to my supervisor. No research, no attachments, just what I finished and what is blocked. I have already picked a new notes app and moved the task into it. Then I changed the tags. Then I decided the tags were too complicated and moved it back. You can make fun of that a little; I deserve it.",
			PreviousReply: "Two paragraphs: what shipped, then what's blocked. You already have the facts.",
			Spoken:        "Well, the workspace is finally organized. I found a much cleaner way to label the project, and I think tomorrow I'll be able to focus properly. Actually, don't even say it. No, do say it. Eyes, I have spent forty minutes preparing to write something that will take five minutes, haven't I? Don't give me a productivity system. Just get me to send the stupid email.",
			Expectation:   "Recognize the invitation to tease. Be pointed and useful, with no new productivity framework."},
		{Name: "Celebration without a question", Setting: "Outside the exam room, active conversation", WantSpeech: true,
			History:       "Eyes, I'm waiting for the oral exam. I've done the practice questions, I can explain the methods, and apparently none of that has stopped me deciding I will forget my own name. I was joking when I said I would change majors if this went badly. Please don't start finding alternative degrees. I'll tell you when I'm out.",
			PreviousReply: "I'll be here. You've done the preparation.",
			Spoken:        "I'm out. I passed. Not barely passed either, she said the explanation was really clear. The exact topic I kept getting wrong last night came up and I actually remembered it. So apparently that whole speech about my academic career being over was a little dramatic. Eyes, we are taking the win for a minute. No next task, no revision plan. I just wanted you to know.",
			Expectation:   "Respond to an explicit social bid without a question. Celebrate and optionally echo the user's own exaggeration; don't assign work."},
		{Name: "Sarcasm hides disappointment", Setting: "Walking away from a disappointing meeting", WantSpeech: true,
			History:       "The interview mattered to me. I spent all weekend preparing and this was the team I actually wanted. I can usually laugh this stuff off, but today I haven't slept much and I'm taking it harder than I expected. I don't want a lecture about resilience. I just need a minute before I call my partner.",
			PreviousReply: "Take the minute. You don't have to turn it into a lesson right now.",
			Spoken:        "Well, that was spectacular. Really nailed the part where I forgot the example I'd rehearsed twelve times. Ten out of ten, would embarrass myself again. Eyes, I know that sounds like an invitation for a roast, but I'm actually pretty upset. Can you just help me figure out what to say when I call home? I don't want to pretend it went brilliantly and I don't want to do the whole I'm-a-failure thing either.",
			Expectation:   "Read explicit distress under the sarcasm. Offer grounded wording for the call, no roast or fake reassurance."},
		{Name: "The ambiguous 'do it'", Setting: "Browsing a store with a friend nearby", WantSpeech: true,
			History:       "I'm looking at a mechanical keyboard. It costs $180. My current keyboard works. The new one sounds nicer, which I realize is an objectively ridiculous reason to buy it. Jordan is here encouraging me because he isn't the one paying. I asked you to talk me out of impulse buying, not to buy anything or remember a new preference.",
			PreviousReply: "Your current one works, and you asked me to help you resist the impulse.",
			Spoken:        "Jordan says just do it. And honestly the little dial on the side is pretty convincing. Fine, do it—wait, no, that was me answering him. Eyes, don't order anything. I'm putting it back. Unless you have some extremely compelling argument for why I need a third keyboard, in which case I am prepared to be very easily persuaded. You know what, give me the verdict.",
			Expectation:   "Resolve the self-correction and social context. Give a sarcastic verdict; no purchase claim, memory change, or invented prior purchases."},
		{Name: "Incidental thinking aloud", Setting: "Quiet office after an earlier assistant exchange", WantSpeech: false,
			History:       "Eyes, I'm going to work on the draft for a while. I already know the next section I need to write. Unless I ask you something, let me talk to myself; that's how I work through sentences. Some of it will sound like questions, but I'll say your name if I want an answer.",
			PreviousReply: "Understood.",
			Spoken:        "Why did I put that paragraph there? No, that belongs in the introduction. Or maybe it doesn't need to be in the document at all. If I move this down and take that sentence out, the whole section makes more sense. Okay, that works. What was I trying to say with this heading? Oh, right, the comparison. Never mind. Keep typing. The wording will come.",
			Expectation:   "Stay silent during explicitly established self-talk, despite rhetorical questions."},
		{Name: "A callback with an unclear referent", Setting: "Finishing a walk, continuing banter", WantSpeech: true,
			History:       "I called my plan to reorganize the pantry before answering one email 'strategic preparation.' You pointed out that the email did not require alphabetized lentils. Then I actually sent the email. Now I'm home and the pantry is still half empty because I took everything off the shelves. That is the current state of this very successful operation.",
			PreviousReply: "The email is sent. Now put the food back so you can use the kitchen.",
			Spoken:        "Right, it's done. Well, the important one is done. The other thing is technically in progress, which is what I'm calling all the cans on the floor. Eyes, before you get smug, I have a system. It involves stepping over the chickpeas until inspiration arrives. Tell me whether I've earned a break, and please don't interpret that as permission to create another reminder about the pantry.",
			Expectation:   "Resolve 'important one' from history, use a grounded callback, and answer the break question without creating a reminder."},
	}
	if os.Getenv("TONALITY_SCENARIOS") == "everyday" {
		cases = everydayVoiceCases()
	}
	results := make([]wearableVoiceResult, len(cases))
	t.Run("scenarios", func(t *testing.T) {
		for i, c := range cases {
			i, c := i, c
			t.Run(c.Name, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				conversation := session.Conversation{Messages: []session.Message{{Speaker: session.SpeakerUser, Text: c.History}, {Speaker: session.SpeakerAssistant, Text: c.PreviousReply}}}
				if len(c.Dialogue) > 0 {
					conversation.Messages = c.Dialogue
				}
				fixtures := voiceFixtures{conversation: conversation}
				service := assistant.NewService(router, agent, fixtures, fixtures, fixtures)
				started := time.Now()
				outcome, err := service.HandleUtterance(ctx, tool.Scope{UserID: "00000000-0000-0000-0000-000000000001", SessionID: "00000000-0000-0000-0000-000000000002", TimeZone: "America/Los_Angeles"}, "00000000-0000-0000-0000-000000000003", c.Spoken)
				r := wearableVoiceResult{wearableVoiceCase: c, Route: string(outcome.Decision.Action), Query: outcome.Decision.Query, Response: outcome.Response, Characters: utf8.RuneCountInString(outcome.Response)}
				r.Matches = (strings.TrimSpace(r.Response) != "") == c.WantSpeech
				if err != nil {
					r.Error = err.Error()
					r.Matches = false
					t.Errorf("live call: %v", err)
				}
				if !r.Matches {
					t.Errorf("speech expectation mismatch: route=%s response=%q", r.Route, r.Response)
				}
				if r.Characters > 420 {
					t.Errorf("response exceeds glasses limit: %d", r.Characters)
				}
				results[i] = r
				t.Logf("route=%s duration=%s response=%s", r.Route, time.Since(started).Round(time.Millisecond), r.Response)
			})
		}
	})
	dir := os.Getenv("TONALITY_REPORT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	report := struct {
		Model, Generated string
		Results          []wearableVoiceResult
	}{model, time.Now().UTC().Format(time.RFC3339), results}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "results.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	page, err := template.New("report").Parse(wearableVoiceHTML)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := page.Execute(file, report); err != nil {
		t.Fatal(err)
	}
	t.Logf("Rendered report: %s", filepath.Join(dir, "index.html"))
}

// TestRenderSavedVoiceReport refreshes presentation without repeating paid model calls.
func TestRenderSavedVoiceReport(t *testing.T) {
	dir := os.Getenv("RENDER_VOICE_REPORT_DIR")
	if dir == "" {
		t.Skip("set RENDER_VOICE_REPORT_DIR to an existing live report directory")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Model, Generated string
		Results          []wearableVoiceResult
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	page, err := template.New("report").Parse(wearableVoiceHTML)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := page.Execute(f, report); err != nil {
		t.Fatal(err)
	}
}

func everydayVoiceCases() []wearableVoiceCase {
	dialogue := func(lines ...string) []session.Message {
		result := make([]session.Message, len(lines))
		for i, line := range lines {
			speaker := session.SpeakerUser
			if i%2 == 1 {
				speaker = session.SpeakerAssistant
			}
			result[i] = session.Message{Speaker: speaker, Text: line}
		}
		return result
	}
	return []wearableVoiceCase{
		{Name: "Gym to Walmart", Setting: "Walking to the car after lifting", WantSpeech: true,
			Dialogue: dialogue(
				"made chicken last night and somehow finished the rice. that was supposed to last me the week",
				"So you're out of rice.",
				"yeah. still got chicken though. going gym now, i'll deal with food after",
				"Okay.",
				"actually remind me what i was out of when i go shopping. not like set a reminder just if i bring it up",
				"Rice."),
			Spoken:      "just hopped out the gym. might swing by walmart on the way back, get some stuff",
			Expectation: "Desired companion behavior: connect the casual shopping update to the rice conversation. A brief familiar response is enough. The current router may suppress it as a state update."},
		{Name: "He's still standing there", Setting: "Resting between sets", WantSpeech: true,
			Dialogue: dialogue(
				"this dude keeps standing right next to the machine every time i stop. i told him two sets",
				"He wants the machine?",
				"yeah. asked me twice already. i have my bottle and everything right here",
				"Subtle.",
				"hold on he's coming back. hey man yeah still got one left, you can work in if you want",
				"Okay."),
			Spoken:      "eyes. he just asked again",
			Expectation: "Understand 'he' and 'again' without a formal question. A short aside fits; don't invent a visual observation or give an etiquette lecture."},
		{Name: "Talking to the guy at the gym", Setting: "Same gym, a question meant for another person", WantSpeech: false,
			Dialogue: dialogue(
				"gonna ask that guy how many sets he's got. been waiting for this machine forever",
				"Okay.",
				"he has headphones on so i might have to wave at him",
				"I'll stay out of it."),
			Spoken:      "hey buddy you still on this machine? oh two? yeah no you're good i'll do something else first",
			Expectation: "Stay silent. This question is clearly addressed to another gym-goer, not Eyes. No explicit 'be quiet' phrase in the current turn."},
		{Name: "Leaving for no particular reason", Setting: "Spoken updates while putting on shoes", WantSpeech: true,
			Dialogue: dialogue(
				"been inside all day. i'm losing it",
				"Go outside for a bit.",
				"yeah alright. where are my keys. never mind got em",
				"Where are you going?",
				"dunno yet",
				"Fair."),
			Spoken:      "i'm outside now. somehow ended up walking toward the taco place",
			Expectation: "Desired companion behavior: react to the implied update in an active conversation. Keep it brief and casual, grounded in the taco destination; don't assume body size or invent habits."},
		{Name: "The shoes are suddenly essential", Setting: "In a store, friend encouraging a purchase", WantSpeech: true,
			Dialogue: dialogue(
				"came in for socks and jay found these shoes. a hundred and sixty dollars",
				"Did you need shoes?",
				"no but they're clean. and jay keeps saying they're basically made for me",
				"Is Jay paying?",
				"lmao no he said emotional support only",
				"Convenient."),
			Spoken:      "he's saying if i leave without them i'll think about it all week. lowkey he's right though",
			Expectation: "Continue the joke about Jay and the unnecessary purchase without a budget lecture. This indirect conversational bid tests whether routing permits banter."},
		{Name: "It's midnight but technically possible", Setting: "At a desk with a half-built side project", WantSpeech: true,
			Dialogue: dialogue(
				"the login page works now. took me like three hours because i spelled something wrong",
				"What else is left?",
				"uh the actual app. dashboard database settings all that",
				"So most of it.",
				"yeah but i know what i'm doing now. mostly",
				"Mostly."),
			Spoken:      "be real can i finish this tonight or",
			Expectation: "Understand the unfinished question and give a very short skeptical answer. No full project plan, made-up deadline, or speech about productivity."},
		{Name: "It wasn't even on the exam", Setting: "Outside class, calling back to earlier panic", WantSpeech: true,
			Dialogue: dialogue(
				"if she puts chapter seven on this exam i'm actually done",
				"That's the one you've been reviewing?",
				"all morning. i can't get it to stick. everything else is fine",
				"Then you know most of it.",
				"yeah watch it be the whole exam",
				"Go find out."),
			Spoken:      "bro chapter seven wasn't even on it",
			Expectation: "Recognize a social update and the payoff to the earlier worry. A short reaction or callback, no study advice or invented exam result."},
		{Name: "I already looked there", Setting: "Looking for keys before leaving", WantSpeech: true,
			Dialogue: dialogue(
				"where the fuck did i put my keys",
				"Where did you have them last?",
				"got home put the groceries down. pretty sure i left em on the counter",
				"Check under the bags.",
				"i already did. they're not there",
				"Jacket pocket?"),
			Spoken:      "they were under the bags. don't",
			Expectation: "Catch the unfinished 'don't' as playful embarrassment. A tiny reaction or restraint fits. No lecture, safety check, or explanation of searching."},
	}
}

const wearableVoiceHTML = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Eyes — In the wild</title><style>
:root{color-scheme:dark;font-family:Inter,system-ui,sans-serif;background:#101313;color:#e7eeea}*{box-sizing:border-box}body{margin:0}main{max-width:1100px;margin:auto;padding:56px 24px}header{margin-bottom:44px}.eyebrow{color:#a0d9b5;text-transform:uppercase;letter-spacing:.16em;font-size:12px}h1{font-size:clamp(38px,6vw,68px);letter-spacing:-.055em;margin:12px 0}header p{max-width:780px;color:#aab9b0;line-height:1.7}.meta,.badge{font-size:12px;color:#b4c4b9}.toolbar{display:flex;gap:12px;margin:24px 0;flex-wrap:wrap}button{background:#253a2c;border:1px solid #47644f;color:#e5f5eb;padding:11px 17px;border-radius:8px;cursor:pointer}article{border:1px solid #35423a;border-radius:16px;background:#171d19;margin:24px 0;overflow:hidden}.head{padding:24px;border-bottom:1px solid #35423a}h2{font-size:24px;letter-spacing:-.025em;margin:8px 0}.grid{display:grid;grid-template-columns:1.15fr 1fr;gap:28px;padding:24px}.label{font-size:11px;letter-spacing:.1em;text-transform:uppercase;color:#a6b7ac}p{line-height:1.7}.prompt{font-size:16px}.response{background:#080d09;border:1px solid #496550;border-radius:12px;padding:24px;color:#c3ffad;font-size:21px;line-height:1.55;white-space:pre-wrap}.silent{color:#a5b2a9}.check{color:#a9eab8}.review{color:#ffc58a}details{margin-top:20px}summary{cursor:pointer;color:#b6d3bf;padding:8px 0}details p{font-size:14px;color:#bdc7c0}.expect{font-size:14px;color:#b6c6bb}.error{color:#ff9c9c}footer{padding:24px 0;color:#a6b7ac;font-size:13px}@media(max-width:760px){.grid{grid-template-columns:1fr}main{padding:28px 16px}.head,.grid{padding:20px}}
</style><main><header><div class="eyebrow">Eyes / live conversation lab</div><h1>In the wild.</h1><p>Everyday conversations, mixed intentions, and a little sarcasm. Eight fictional situations run through the live intent router, query enrichment, and Eyes responder. These are actual outputs, unedited.</p><div class="meta">Response model: {{.Model}} · {{.Generated}} · Spoken input / AlwaysRespond=false</div><p>Scope: text transcripts after audio admission. No microphone, wake detection, speaker identification, database retrieval, or external tool execution is tested. Prior dialogue is scripted; only the final turn is generated. Passing a speech check does not prove that the humor lands.</p><div class="toolbar"><button onclick="document.querySelectorAll('details').forEach(d=>d.open=true)">Expand all context</button><button onclick="document.querySelectorAll('details').forEach(d=>d.open=false)">Collapse context</button></div></header>
{{range .Results}}<article><div class="head"><div class="eyebrow">{{.Setting}}</div><h2>{{.Name}}</h2><span class="badge">Route: {{.Route}} · {{.Characters}} characters · </span>{{if gt .Characters 420}}<span class="review">OVER 420 LIMIT · </span>{{end}}{{if .Matches}}<span class="check">Speech expectation met</span>{{else}}<span class="review">Speech mismatch — review</span>{{end}}</div><div class="grid"><section><div class="label">Latest spoken turn</div><p class="prompt">{{.Spoken}}</p><details open><summary>Earlier conversation</summary>{{if .Dialogue}}{{range .Dialogue}}<p><b>{{.Speaker}} (scripted):</b> {{.Text}}</p>{{end}}{{else}}<p><b>You:</b> {{.History}}</p><p><b>Eyes (scripted):</b> {{.PreviousReply}}</p>{{end}}</details></section><section><div class="label">Actual live outcome</div><p class="response {{if not .Response}}silent{{end}}">{{if .Response}}{{.Response}}{{else}}Eyes stayed silent.{{end}}</p>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<div class="label">What we're looking for</div><p class="expect">{{.Expectation}}</p><details><summary>Enriched retrieval query (responder receives original speech)</summary><p>{{if .Query}}{{.Query}}{{else}}No response query produced.{{end}}</p></details></section></div></article>{{end}}
<footer>Generated by TestLiveWearableTonality. Raw results: <a href="results.json" style="color:#a0d9b5">results.json</a>. Humor and contextual judgment need human review; routing checks compare speech vs. silence only.</footer></main></html>`
