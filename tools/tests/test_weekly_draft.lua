-- Verify that the activity entry and a complete pick request use the draft protocol.
local req, panel
local modules = {}
local function add(name, data)
  modules[name] = data
  package.loaded[name] = data
end
add("nex_prot", {search_cli_prot_info=function() end, search_serv_prot_info=function() end})
dao_mgr = {getFullActInfoList=function() return {{id=1}} end}
add("logic.dao_mgr", true)
add("dao_mgr", dao_mgr)
add("utils", {getTime=function() return 100 end, message_box=function(text) if text:find("页面加载失败") then error(text) end end, confirm_box=function(_,cb) cb() end})
for i, name in ipairs({"LUNDAO","FUBEN","GOLDTREE","BATTLE","YUANXU","DUOBAO","JOKING","STARWAR","TURNTABLE","TIANTI","XIUXING","TEMPORAL_RIFT","JIUYOU_PRACTICE"}) do
  dao_mgr["ACT_"..name] = i
end
local current, opened = 1, {}
dao_mgr.getCurActIdx = function() return current end
dao_mgr.setCurActIdx = function(i) current = i end
modules.utils.call_out = function(_, cb) cb() end
ui_mgr = {
  PR_2_UI=1, getInst=function() end, closeAll=function() end,
  close=function() end,
  open=function(name,_,callback)
    opened[#opened+1] = name
    if name == "local_weekly_draft.CDraftView" then
      local class=package.loaded.local_weekly_draft.CDraftView
      -- ui_mgr routes unknown UI_FILE names to the default layer; the activity
      -- screen above that layer hides the new page unless it opts into MID.
      local layer=class.UI_LAYER or game.UI_LAYER
      assert(layer==game.UI_MID_LAYER,"draft page is behind the activity screen")
      if not panel then
        panel = package.loaded.local_weekly_draft.CDraftView:createInst({})
        panel:init()
      end
      panel:setVisible(true)
      if callback then callback(panel) end
      return panel
    end
    return {setCurItem=function() end}
  end
}
CObject = {new=function(self,t) return setmetatable(t or {},{__index=self}) end}
game = {UI_LAYER={},UI_MID_LAYER={},UI_MINOR_LAYER_2={}}
MyData = {grade=50}
for _, name in ipairs({"ui","uicreator","logic.red_mgr","screenadapt_mgr"}) do add(name,{}) end
assert(loadfile("../client2/apk-src/assets/script/view/CMainActBottomView.lua"))("view.CMainActBottomView")
add("view.CMainActBottomView", view.CMainActBottomView)
local function widget()
  return {
    getSize=function()return 112,110 end, getPosition=function()return 16,6 end, topmost=function()end,
    setSize=function(s,w,h)s.width,s.height=w,h end,
    setImage=function(s,v)s.image=v;if s.baseInst then s.baseInst.rect=nil end end, setColor=function(s,v)s.color=v end,
    setVisible=function(s,v)s.visible=v end, setEnable=function(s,v)s.enabled=v end,
    setText=function(s,v)s.text=v end, setTouchEnabled=function()end,
    setPosition=function(s,x,y)s.x,s.y=x,y end,
    setData=function(s,v)s.data=v end, setHighlight=function(s,v)s.highlight=v end,
    setTalentVisible=function()end, setFlag=function(s,v)s.flag=v end
  }
end
modules.ui.CLabel = {create=function()return widget()end}
modules.uicreator.createPanelWithMap = function(parent,resource,class)
  assert(resource.MainMap.OffsetX1==640 and resource.MainMap.OffsetY1==960)
  local p=setmetatable(widget(),{__index=class})
  for _,key in ipairs({"btnHelp","btnDraftTab","btnRankTab","btnLastTab","lblStatus","lblHint","btnJoin","btnOK","btnSelect","btnOneKey","btnCancel","btnClose","lblTitle","lblDesc"})do p[key]=widget() end
  p.ctlTeam=widget()
  p.ctlTeam.fightPosition,p.ctlTeam.fightPositionPosition={},{}
  for i=1,9 do
    p.ctlTeam.fightPosition[i]=widget()
    p.ctlTeam.fightPositionPosition[i]={x=i,y=i<6 and 0 or 100}
  end
  return p
end


local detailCard,detailData
add("logic.pet_mgr",true)
pet_mgr={newPet=function(data)return data end}
add("logic.suit_mgr",true)
add("logic.magic_mgr",true)
suit_mgr={newSuit=function(data)
 assert(getmetatable(data)==nil)
 data.model=10001
 return setmetatable(data,{__index={isGameItem=true}})
end}
magic_mgr={newMagic=function(data)
 assert(getmetatable(data)==nil)
 data.model=20001
 return setmetatable(data,{__index={isGameItem=true}})
end}
local open=ui_mgr.open
ui_mgr.open=function(name,flag,callback)
 if name=="view.PetEasyDetailView.CPetEasyDetailMainView" then
  return {setData=function(_,data)detailCard=data.type;detailData=data end}
 end
 return open(name,flag,callback)
end
modules.uicreator.createPanelWithFile=function(parent,file,class)
 return modules.uicreator.createPanelWithMap(parent,package.loaded["ui."..file],class)
end
add("view.StarWarTeamView",{})
add("view.CommonItem",{setGray=function(icon,b)icon.gray=b end})
view.CPetView={CPetIconInfo={create=function()return {}end}}
add("view.CPetView",view.CPetView)
datatable_mgr={getDataInfoByType=function(_,id)return {shape=id,star=5}end}
local decoded
add("dkjson", {decode=function() return decoded end})
net = {send_prot=function(spec, body) req = {spec=spec,body=body} end}
local battleFinishHandlers={}
SIG={FINISH_FIGHT="finish_fight"}
add("signal",{reg=function(name,cb)
 assert(name==SIG.FINISH_FIGHT)
 battleFinishHandlers[#battleFinishHandlers+1]=cb
end})
local function finishFight()for _,cb in ipairs(battleFinishHandlers)do cb()end end
dofile("../client2/fixes/bootstrap.lua")
dofile("../client2/fixes/weekly_draft.lua")
local installed = package.loaded.local_weekly_draft
dofile("../client2/fixes/bootstrap.lua")
dofile("../client2/fixes/weekly_draft.lua")
assert(package.loaded.local_weekly_draft == installed)
local row = dao_mgr.getFullActInfoList()[2]
assert(row.name == "轮抓")
local menu = modules["view.CMainActBottomView"]
menu.openView(1)
assert(opened[1] == "view.RankView.CRankMainView")
local entry=setmetatable(widget(),{__index=menu.CGameActBottomItemView})
CCRect=function(x,y,w,h)return {origin={x=x,y=y},size={width=w,height=h}}end
entry.imgIcon=widget()
entry.imgIcon.baseInst={getTextureRect=function(s)return s.rect or CCRect(10,20,80,102)end,setTextureRect=function(s,r)s.rect=r end}
entry.setData=menu.CGameActBottomItemView.setData;entry:setData(row)
entry:setActive(false)
assert(entry.draftLabel.text=="轮抓" and entry.draftLabel.color==16777215 and entry.imgIcon.image=="ui/activity/xingzhan00.png" and entry.imgIcon.baseInst.rect.size.height==74)
entry:setActive(true)
assert(entry.draftLabel.color==16766720 and entry.imgIcon.image=="ui/activity/xingzhan01.png")
entry:setData({id=1,icon="normal.png"})
assert(not entry.draftLabel.visible and not entry.imgIcon.gray and entry.imgIcon.width==112 and entry.imgIcon.y==6)
entry:setData(row)
local bottom=setmetatable({curActIdx=1,data={{id=1},row},esrvList={childItems={entry,entry},showItemByIdx=function()end}}, {__index=menu.CGameActBottomView})
bottom:onItemClicked(2)
assert(current==2 and bottom.curActIdx==2 and panel.visible and panel.lblDesc.text=="正在加载轮抓数据…")
assert(req.spec.SUBCMD == 97001 and req.body.action == 0)
local spec = modules.nex_prot.search_serv_prot_info(97,1)
local function receive(state)
  -- Real dkjson objects have metadata even before they become item models.
  for _,info in pairs(state.details or {}) do
    for _,key in ipairs({"suits","magics"}) do
      for _,row in ipairs(info[key] or {}) do setmetatable(row,{__jsontype="object"}) end
    end
  end
  decoded = state
  package.loaded[spec.MOD][spec.FUNC]({state=""})
end
receive({uid=1,week="w",round=1,pack={11,12,13,14,15,16},names={["16"]="候选六"},details={["16"]={att=1715,hp=7930,speed=7,magicLevel=6,talents={1,2,3},suits={{type=1,grade=40},{type=2,grade=40},{type=0}},magics={{type=3,grade=6,jxgrade=3}}}},score=1000,fights=20,skips=5})
local icons=panel.ctlTeam.fightPosition
assert(icons[1].data.icon==11 and icons[6].data.icon==16 and not icons[7].visible)
assert(icons[1].x==2 and icons[6].x==8)
assert(not panel.btnOK.enabled)
icons[6].onClicked()
assert(panel.btnOK.enabled and panel.lblDesc.text=="已选：候选六" and detailCard==16)
assert(detailData.suits[1].isGameItem and detailData.magics[1].isGameItem and detailData.suits[1].model and detailData.magics[1].model)
assert(detailData.tal_magic_lev==6 and detailData.suits[2].grade==40 and detailData.magics[1].grade==6 and detailData.magics[1].jxgrade==3)
assert(not installed.CDraftView.UI_POP and installed.CDraftView.FULL_SCREEN_COVER)
panel.btnOK.onClick()
assert(req.body.action==2 and req.body.round==1 and req.body.card==16)
receive({uid=1,week="w",round=2,picked={16},pack={31,32,33,34,35,36},score=1000,fights=20,skips=5})
local pickRequest=req
panel.btnCancel.onClick()
assert(panel.btnOK.text=="继续选卡" and req==pickRequest and icons[1].data.icon==16)
panel.btnOK.onClick()
assert(icons[1].data.icon==31 and req==pickRequest and panel.lblTitle.text=="轮抓选卡 2 / 8")
receive({uid=1,week="w",picked={11,12,13,14,15,16,17,18},score=1000,fights=20,skips=5})
assert(icons[8].visible and not icons[9].visible)
assert(icons[1].x==1 and icons[8].x==8 and icons[1].flag)
panel.btnOK.onClick()
assert(req.body.action==4 and req.body.lineup[8].card==18 and req.body.leader==1)
assert(panel.btnSelect.visible and panel.btnOK.visible and panel.btnJoin.visible)
assert(not panel.btnCancel.visible and not panel.btnOneKey.visible)
assert(panel.btnOK.text=="匹配对手" and panel.btnJoin.text=="放弃")
receive({uid=1,week="w",picked={11,12,13,14,15,16,17,18},score=1000,fights=20,skips=5,
  defense={leader=1},match={name="对手",score=1000,lineup={leader=1,cards={21,22,23,24,25,26,27,28}}}})
menu.showAct(2)
receive({uid=1,week="w",picked={11,12,13,14,15,16,17,18},score=1000,fights=20,skips=5,
  defense={leader=1},match={name="对手",score=1000,lineup={leader=1,cards={21,22,23,24,25,26,27,28}}}})
assert(icons[1].data.icon==21 and icons[8].data.icon==28)
panel.btnSelect.onClick()
icons[1].onClicked()
icons[2].onClicked()
assert(icons[1].data.icon==12 and icons[2].data.icon==11)
panel.btnOK.onClick()
panel.btnOK.onClick()
assert(req.body.action==6 and req.body.lineup[1].card==12 and req.body.leader==2)
local fightRequest=req
finishFight()
assert(req~=fightRequest and req.body.action==0 and panel.lblDesc.text=="正在加载轮抓数据…")
local refreshRequest=req
finishFight()
assert(req==refreshRequest,"unrelated/duplicate battle end refreshed draft")
-- A consumed opponent is omitted from JSON. Keep the same week/generation so
-- unrelated reset logic cannot hide the stale match-mode regression.
receive({uid=1,week="w",picked={11,12,13,14,15,16,17,18},score=990,fights=19,skips=5})
assert(panel.ctlTeam.visible and icons[1].data.icon==12 and panel.btnOK.text=="匹配对手")
assert(panel.btnSelect.visible and panel.btnJoin.visible and panel.lblStatus.text:find("990"))
assert(panel.btnOK.enabled and panel.btnSelect.enabled and panel.btnJoin.enabled)
-- A malformed/JSON-null match must behave like no opponent.
receive({uid=1,week="w",picked={11,12,13,14,15,16,17,18},score=990,fights=19,skips=5,match={}})
assert(panel.btnOK.text=="匹配对手" and panel.ctlTeam.visible)
receive({uid=1,week="w",generation=0,picked={11,12,13,14,15,16,17,18},score=1200,fights=17,skips=3})
local previousRequest=req
panel.btnJoin.onClick()
assert(req==previousRequest and panel.btnOK.text=="确认重抓" and panel.btnCancel.text=="取消")
panel.btnCancel.onClick()
assert(req==previousRequest and panel.ctlTeam.visible)
panel.btnJoin.onClick()
panel.btnOK.onClick()
assert(req.body.action==7 and req.body.card==0)
receive({uid=1,week="w",generation=1,round=1,pack={31,32,33,34,35,36},score=1200,fights=17,skips=3})
assert(icons[1].data.icon==31 and not panel.btnOK.enabled)
receive({uid=2,week="w",score=1000,fights=20,skips=5})
assert(icons[1].data.icon==0 and panel.btnOK.text=="开始轮抓")
assert(package.loaded["ui.LocalDraft_Main"].MainMap.Module=="LocalDraft")
assert(package.loaded["ui.LocalDraft_Main"].ButtonHelpMap.Filename=="ui/gongyong_new/btn_gy_new_help.png")
panel.btnHelp.onClick()
assert(panel.lblDesc.text=="轮抓规则" and not panel.btnHelp.visible)
panel.btnCancel.onClick()
panel.btnRankTab.onClick()
assert(panel.lblTitle.text=="本周轮抓榜" and not panel.ctlTeam.visible)
panel.btnDraftTab.onClick()
assert(panel.ctlTeam.visible)
print("PASS: native activity entry, graphical picks, five-plus-three lineup, captain, upload and opponent preview")

-- Use the actual shipped resource builder, including its control naming and
-- init lifecycle. Earlier stubs incorrectly exposed createPanelWithMap, which
-- is not a public method of the Android resource builder.
local noop=function()end
CCApplication={sharedApplication=function()return {getTargetPlatform=function()return 3 end}end}
IsClass=function()return false end
IsClsReservedVarName=function()return false end
add("default",{})
add("res_stat",{addResEnt=noop})
modules.utils.is_mini_app=function()return false end
modules.screenadapt_mgr.getUpDis=function()return 0 end
modules.screenadapt_mgr.getDownDis=function()return 0 end
local function nativeWidget(parent)
 local w=widget();w.parent=parent
 w.setAnchorPoint=noop;w.setOpacity=noop;w.setVerticalAlignment=noop;w.setHorizontalAlignment=noop
 return w
end
modules.ui.CPanel={create=function(_,parent)return nativeWidget(parent)end}
modules.ui.CScaleImage={create=function(_,parent)return nativeWidget(parent)end}
modules.ui.CTextButton={create=function(_,parent)return nativeWidget(parent)end}
modules.ui.CLabel={create=function(_,parent)return nativeWidget(parent)end}
modules["view.StarWarTeamView"].CWarTeamGroupView={createInst=function(_,parent)
 return modules.uicreator.createPanelWithMap(parent,package.loaded["ui.LocalDraft_Main"],{}).ctlTeam
end}
utils=modules.utils;screenadapt_mgr=modules.screenadapt_mgr;default=modules.default
local nativeBuilder=assert(loadfile("../client2/apk-src/assets/lib/uicreator.lua"))()
assert(not nativeBuilder.createPanelWithMap)
local nativePage=nativeBuilder.createPanelWithFile(game.UI_MID_LAYER,"LocalDraft_Main",installed.CDraftView)
nativePage:render()
assert(nativePage.btnDraftTab and nativePage.btnRankTab and nativePage.lblStatus and nativePage.ctlTeam and not nativePage.btnClose)
assert(nativePage.lblTitle.text=="轮抓" and nativePage.btnOK.text=="开始轮抓")
print("PASS: dedicated draft page builds and renders through the shipped Android uicreator")

-- Exercise the shipped equipment/spell detail renderer: JSON object metadata
-- used to bypass newSuit/newMagic and abort refresh before layout was updated.
assert(loadfile("../client2/apk-src/assets/script/view/PetEasyDetailView.lua"))("view.PetEasyDetailView")
local detailView=view.PetEasyDetailView.CPetEasyDetailSuitMagicView
local controls={widgets={}}
for _,name in ipairs({"ctlSuit1","ctlSuit2","ctlSuit3","ctlMagic1","ctlMagic2"}) do
 local control={resetWidgets=function()end,setData=function(_,item)assert(item.model,"item model missing")end}
 controls[name]=control
 table.insert(controls.widgets,control)
end
setmetatable(controls,{__index=detailView})
local raw={suits={setmetatable({type=1,grade=40},{__jsontype="object"})}}
assert(not pcall(function()controls:setData(raw)end),"old JSON-item failure was not reproduced")
controls:setData(detailData)
print("PASS: reproduced JSON metatable crash; shipped detail renderer accepts materialized draft equipment and spells")
