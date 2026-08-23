package bootstrap

import "testing"

func TestInitialSitesIncludeRequestedSources(t *testing.T) {
	want := map[string]struct {
		name            string
		source          string
		adapter         string
		adapterConfig   string
		sessionRequired bool
	}{
		"https://muyuan.do":                      {name: "君の公益", source: "https://muyuan.do/pricing", adapter: "newapi-pricing", adapterConfig: `{"availabilityMode":"presence","skipDetails":true,"pricingRequiresSession":true}`, sessionRequired: true},
		"https://new-api.abrdns.com":             {name: "new-api abrdns", source: "https://new-api.abrdns.com/status", adapter: "newapi-probe", adapterConfig: `{"pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true,"pricingRequiresSession":true}`, sessionRequired: true},
		"https://api.fengwind.com":               {name: "fengwind", source: "https://api.fengwind.com/model-market", adapter: "model-market", adapterConfig: `{"pricingAdapter":"model-market"}`, sessionRequired: true},
		"https://api.42w.shop":                   {name: "42w", source: "https://api.42w.shop/pricing", adapter: "newapi-pricing", sessionRequired: true},
		"https://52ccl.net":                      {name: "月城公益站", source: "https://52ccl.net/pricing", adapter: "newapi-pricing", sessionRequired: true},
		"https://ai.121628.xyz":                  {name: "霸气公益平台", source: "https://ai.121628.xyz/pricing", adapter: "newapi-pricing", sessionRequired: true},
		"https://v-api.de5.net":                  {name: "V - API", source: "https://v-api.de5.net/pricing", adapter: "newapi-pricing", sessionRequired: true},
		"https://yetoken.cc.cd":                  {name: "Yetoken", source: "https://yetoken.cc.cd/pricing", adapter: "newapi-pricing"},
		"https://cngov.cc.cd":                    {name: "CN Gov", source: "https://cngov.cc.cd/pricing", adapter: "newapi-pricing"},
		"https://jiuuij.de5.net":                 {name: "Joverna", source: "https://jiuuij.de5.net/model-health", adapter: "newapi-pricing", adapterConfig: `{"skipDetails":true}`},
		"https://api.hlool.top":                  {name: "Hlool API", source: "https://api.hlool.top/pricing", adapter: "newapi-pricing"},
		"https://x666.me":                        {name: "薄荷 API", source: "https://x666.me/", adapter: "newapi-probe", adapterConfig: `{"statusBaseUrl":"https://tool.x666.me","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true}`},
		"https://runanytime.hxi.me":              {name: "HXI AI", source: "https://stat.hxi.me/status/ai", adapter: "uptime-kuma", adapterConfig: `{"statusBaseUrl":"https://stat.hxi.me","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status","pricingOptional":true,"pricingRequiresSession":true}`},
		"https://jianzhile.vip":                  {name: "jianzhile.vip", source: "https://jianzhile.vip/console/model-status", adapter: "model-probe", adapterConfig: `{"modelProbePath":"/api/model_probe/status","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`, sessionRequired: true},
		"https://sub2.pigeonw.com":               {name: "Pigeon Sub2API", source: "https://sub2.pigeonw.com/monitor", adapter: "sub2api-monitor", adapterConfig: `{"monitorPath":"/api/v1/channel-monitors"}`, sessionRequired: true},
		"https://api.2020111.xyz":                {name: "老魔公益站", source: "https://api.2020111.xyz/pricing", adapter: "newapi-pricing"},
		"https://xn--wnup5g6so4wn.de5.net":       {name: "PM-API", source: "https://xn--wnup5g6so4wn.de5.net/pricing", adapter: "newapi-pricing"},
		"https://windhub.cc":                     {name: "Ark API", source: "https://windhub.cc/model-status", adapter: "newapi-probe", adapterConfig: `{"catalogPath":"/api/enhancements/model-status/embed/status/all","statusPath":"","detailPath":"","detailPathTemplate":"/api/enhancements/model-status/embed/status/{model}?window=24h","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`},
		"https://api-yi-hydrogel.seeseed1ck.icu": {name: "773 公益站", source: "https://api-yi-hydrogel.seeseed1ck.icu/pricing", adapter: "newapi-pricing"},
		"https://aiapi.exe.xyz":                  {name: "AIAPI", source: "https://aiapi.exe.xyz/jk", adapter: "aiapi-probe", adapterConfig: `{"statusPath":"/jk/api/status","period":"24h","board":"hot"}`},
		"https://x-api.cfd":                      {name: "X-API", source: "https://x-api.cfd/pool", adapter: "xapi-pool"},
		"https://api.ark717.com":                 {name: "小鸡毛的公益API站", source: "https://api.ark717.com/pricing", adapter: "newapi-pricing"},
		"https://ai.prism.uno":                   {name: "PrismAI", source: "https://ai.prism.uno/pricing", adapter: "newapi-pricing"},
		"https://wududu.edu.kg":                  {name: "dudu公益站", source: "https://wududu.edu.kg/pricing", adapter: "newapi-pricing"},
		"https://status.coee.ccwu.cc":            {name: "CoeeApi", source: "https://api.coee.ccwu.cc/", adapter: "uptime-kuma", adapterConfig: `{"slug":"check","retryAttempts":3,"monitorNameMode":"suffix-model"}`},
		"https://luckyg.131518.xyz":              {name: "luckyg", source: "https://luckyg.131518.xyz/pricing", adapter: "newapi-pricing"},
		"https://pavv.me":                        {name: "Pavv", source: "https://pavv.me/pricing", adapter: "newapi-pricing"},
		"https://newapi.imagic.eu.org":           {name: "imagic", source: "https://newapi.imagic.eu.org/pricing", adapter: "newapi-pricing"},
		"https://api.fengshao1227.com":           {name: "咕咕嘎嘎", source: "https://api.fengshao1227.com/status", adapter: "model-pulse", adapterConfig: `{"pulsePath":"/api/model-pulse","pricingAdapter":"newapi","pricingPath":"/api/pricing","pricingStatusPath":"/api/status"}`, sessionRequired: true},
	}
	got := make(map[string]SiteSeed, len(InitialSites))
	for _, site := range InitialSites {
		got[site.BaseURL] = site
	}
	for baseURL, expected := range want {
		site, ok := got[baseURL]
		if !ok {
			t.Fatalf("requested site %q is missing", baseURL)
		}
		if site.Name != expected.name || site.SourceURL != expected.source || site.Adapter != expected.adapter || site.AdapterConfig != expected.adapterConfig || site.SessionRequired != expected.sessionRequired {
			t.Errorf("site %q = %+v, want name=%q source=%q adapter=%q config=%q", baseURL, site, expected.name, expected.source, expected.adapter, expected.adapterConfig)
		}
	}
}

func TestDefaultAdapterConfig(t *testing.T) {
	if got := defaultAdapterConfig(""); got != `{}` {
		t.Fatalf("empty adapter config = %q, want {}", got)
	}
	if got := defaultAdapterConfig(`{"skipDetails":true}`); got != `{"skipDetails":true}` {
		t.Fatalf("configured adapter config changed: %q", got)
	}
}
