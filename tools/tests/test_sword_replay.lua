local played, advanced = 0, 0
local sword = {run=function(self) played = played + self.data.count end}
package.loaded["war.netwar"] = {
  CSwordAttack=sword, CurrWar={nextCmd=function() advanced = advanced + 1 end}
}
dofile("../client2/fixes/bootstrap.lua")
dofile("../client2/fixes/sword_replay.lua")
sword.run({data={count=255}})
sword.run({data={count=0}})
assert(played == 0 and advanced == 2)
sword.run({data={count=3}})
assert(played == 3 and advanced == 2)
print("PASS: malformed sword replay advances, valid animation is preserved")
