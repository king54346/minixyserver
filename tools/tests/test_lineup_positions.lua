-- Runs the shipped multiteam bytecode, retaining its cache/save implementation.
package.loaded.utils={}
signal={reg=function() end}
MyData={grade=50}
PT={LINEUP_PTO={C_SET_LINEUPS={},C_CALC_FIGHTPOINT={},C_ONE_CLICK_EQUIP={}}}
local sent
net={send_prot=function(p,d) sent=d; return 'sent' end}
package.loaded.net=net
assert(loadfile('../client2/apk-src/assets/script/logic/multiteam.lua'))('logic.multiteam')
local m=logic.multiteam
local hooks={}
function __onreq(n,f)
  hooks[n]=hooks[n] or {}; table.insert(hooks[n],f)
  if package.loaded[n] then f(package.loaded[n]) end
end
if arg[1]~='old' then
  dofile(arg[1] or '../client2/fixes/lineup_positions.lua')
  dofile(arg[1] or '../client2/fixes/lineup_positions.lua')
end
for _, pos in ipairs({0,1,127,128,255,256,300,511,512,768,1000,65535}) do
  local function slot(p)
    return {pos=1,pet=1,spos1=p%256,spos2=p%256,spos3=p%256,
      s_talent_1=p>255 and 32768+math.floor(p/256) or 0,
      s_talent_2=p>255 and 32768+math.floor(p/256) or 0,
      s_talent_3=p>255 and 32768+math.floor(p/256) or 0,mpos1=7,mpos2=8}
  end
  m.S_SEND_LINEUP({fno=1,count=3,lineups={{lineup_no=1,leader=1,info={slot(pos)}}}})
  assert(m.getTeamPosData(1,1).spos1==pos,'uint8 lineup position wrapped: '..pos)
  assert(m.C_SET_LINEUPS(1)==nil)
  assert(sent~=m.getTeamData(1))
  for i=1,3 do
    local r=sent.lineups[1].info[1]
    assert(r['spos'..i]+(r['s_talent_'..i]-32768)*256==pos,'outbound position lost')
    assert(m.getTeamPosData(1,1)['spos'..i]==pos,'packing mutated cached lineup')
  end
  assert(sent.lineups[1].info[1].mpos2==8)
  m.S_SEND_ONE_CLICK_EQUIP_LINEUP({lineups={{lineup_no=2,leader=1,info={slot(pos)}}}})
  assert(m.getTeamPosData(2,1).spos1==pos,'one-click position lost')
  for _, name in ipairs({'C_CALC_FIGHTPOINT','C_ONE_CLICK_EQUIP'}) do
    local data={lineups={{info={{spos1=pos,spos2=0,spos3=0}}}}}
    assert(net.send_prot(PT.LINEUP_PTO[name],data)=='sent')
    assert(data.lineups[1].info[1].spos1==pos,'preview mutated editor')
    assert(sent.lineups[1].info[1].spos1+(sent.lineups[1].info[1].s_talent_1-32768)*256==pos)
  end
end
local unrelated={x=1}
local row={pos=1,pet=1,spos1=44,spos2=45,spos3=46,s_talent_1=32769,s_talent_2=32770,s_talent_3=32771,mpos1=7,mpos2=8}
m.S_SEND_ONE_CLICK_EQUIP_LINEUP({lineups={{lineup_no=2,leader=1,info={row}}}})
local gear=m.getTeamPosData(2,1)
assert(gear.spos1==300 and gear.spos2==557 and gear.spos3==814,'weapon/shirt/shoe mixed or truncated')
CFG={ITK_WEAPON='weapon',ITK_SHIRT='shirt',ITK_SHOE='shoe'}
local bag={
  [35]={kind=CFG.ITK_WEAPON,itemnum=1039},
  [291]={kind=CFG.ITK_SHOE,itemnum=1509}
}
suit_mgr={getSuitByPos=function(pos) return bag[pos] end}
local function receiveShoe(tag)
  m.S_SEND_LINEUP({fno=1,count=3,lineups={{lineup_no=1,leader=1,info={
    {pos=1,pet=1,spos1=35,spos2=0,spos3=35,s_talent_3=tag}
  }}}})
  return m.getTeamPosSuits(1,1)
end
local wear=receiveShoe(32769)
assert(wear.weapon==1039 and wear.shoe==1509,'full bag position did not resolve to the shoe')
wear=receiveShoe(0)
assert(wear.weapon==1039 and wear.shoe==nil,'weapon leaked into the shoe slot')
net.send_prot({},unrelated); assert(sent==unrelated,'unrelated protocol changed')
print('PASS: shipped multiteam bytecode, uint8 boundaries, 300/1000/65535 positions, switching, one-click, previews, cache immutability, idempotence')
