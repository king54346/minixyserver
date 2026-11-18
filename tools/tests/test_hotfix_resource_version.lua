local f=assert(io.open('../client2/apk-src/assets/res.plist','rb'))
local plist=f:read('*a');f:close()
local version=assert(plist:match('<key>Version</key>%s*<string>(.-)</string>'))
assert(version=='1.5.353','review compatibility against the actual APK resource version')
package.loaded.utils={keys=function()return {}end}
table.print=function()end
PT={LOGIN_PTO={C_AFTER_LOGIN={}}};local completed=0
net={send_prot=function()completed=completed+1 end}
CCDictionary={createWithContentsOfFile=function(_,path)
 assert(path=='res.plist')
 return {objectForKey=function(_,key)
  assert(key=='Version');return {getCString=function()return version end}
 end}
end}
tolua={cast=function(x)return x end}
__G__TRACKBACK__=function(e)error(e)end
assert(loadfile('../client2/apk-src/assets/script/hotfix.lua'))('hotfix')
local content='_G.__compat_counter=(_G.__compat_counter or 0)+1'
hotfix.loadHotfix({{hotfix_id='1.5.403',hotfix_content=content}})
assert(__compat_counter==nil,'original 403-only patch unexpectedly ran on this APK')
hotfix.loadHotfix({{hotfix_id='1.5.403',hotfix_content=content},{hotfix_id=version,hotfix_content=content}})
assert(__compat_counter==1 and completed==2,'compatible patch not loaded exactly once')
print('PASS: shipped hotfix loader and actual res.plist '..version..'; 403-only skipped, explicit compatible entry executed once')
