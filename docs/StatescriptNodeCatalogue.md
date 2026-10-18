# Statescript node catalogue (client build 24919)

Generated from the client binary, `systems/data/client_stu_registry.json` and the 296 graphs resident in the Temple of Anubis snapshot. How it was produced and what the columns mean: [StatescriptRuntime.md](StatescriptRuntime.md) §2. Machine-readable form: `systems/data/statescript/statescript_node_catalogue.json`.

Columns: **hash** = STU type hash (CRC32 of the lower-cased name); **name** = OWLib KnownTypes, *italic* = recovered by CRC32 match of a guessed name (likely, not certain; confidence and provenance per name in `systems/data/statescript/statescript_names.csv`), blank = unknown; **STU** = size of the STU (asset) object; **own fields** = fields this type adds to its parent (`+offset name:type`, `*` = object pointer, `[]` = list, `ref` = resource reference; CV = STUConfigVar, SS = STUStatescript); **runtime** = client class: vtable RVA, object size, priority layer (highest wins); `→ X` = no own class, the nearest registered ancestor X is used; **uses** = instances in resident graphs / number of graphs.

| kind | types | used in resident graphs | with own client class |
|---|---|---|---|
| Entry | 3 | 3 | 3 |
| Condition | 5 | 4 | 4 |
| Action | 119 | 100 | 85 |
| Terminator | 3 | 1 | 2 |
| State | 259 | 192 | 185 |
| ConfigVar | 328 | 243 | 285 |
| Base | 2 | 1 | 1 |
| **total** | **719** | **544** | **565** |

## Entries (3)

| hash | name | parent | STU | own fields | runtime | uses |
|---|---|---|---|---|---|---|
| `2f937ae3` | STUStatescriptEntry | SSBase | 0x80 | +78 m_outPlug:SSOutputPlug* | `0x1604ad8` 0x8 p0 | 549 / 253 |
| `be49a4c2` | STUStatescriptEntryGameMessage | SSEntry | 0xc0 | +80 m_params:c679b01f*[], +90 m_gameMessage:CVGameMessage*, +98 m_out_Sender:CVDynamic*, +a0 m_out_Instigator:CVDynamic*, +a8 a6152623:CVDynamic*, +b0 m_filter:CVFilter*, +b8 eb7f6ab1:u8 | `0x16049d8` 0x8 p0 | 488 / 173 |
| `46c48944` | STUStatescriptEntryPlayScript | SSEntry | 0xa0 | +80 m_params:c679b01f*[], +90 m_out_Sender:CVDynamic*, +98 eb7f6ab1:u8 | `0x1615150` 0x8 p1; `0x16048d8` 0x8 p0 | 28 / 27 |

## Conditions (5)

| hash | name | parent | STU | own fields | runtime | uses |
|---|---|---|---|---|---|---|
| `b1032daf` | *STUStatescriptCondition_Bool* | SSCondition | 0x98 | +90 m_condition:CV* | `0x160b2f8` 0x8 p0 | 940 / 198 |
| `5720b660` | *STUStatescriptCondition_ArrayContains* | SSCondition | 0xa8 | +90 edfb51d3:CV*, +98 m_key:CV*, +a0 m_value:CV* | `0x160aff8` 0x8 p0 | 35 / 11 |
| `07722a23` | *STUStatescriptCondition_Timeout* | SSCondition | 0x98 | +90 c4630712:CV* | `0x160b1f8` 0x8 p0 | 12 / 11 |
| `4768e4a3` | *STUStatescriptCondition_LogicalButton* | SSCondition | 0xb0 | +90 c77035be:i32[], +a0 m_logicalButton:CV*, +a8 30c0a41d:u8, +a9 86cd8af0:u8, +aa a3e7d1e9:u8, +ab b4015d42:u8 | `0x160b0f8` 0x8 p0 | 4 / 4 |
| `6f10080f` | STUStatescriptCondition | SSBase | 0x90 | +78 m_inPlug:SSInputPlug*, +80 m_truePlug:SSOutputPlug*, +88 m_falsePlug:SSOutputPlug* | – | – |

## Actions (119)

| hash | name | parent | STU | own fields | runtime | uses |
|---|---|---|---|---|---|---|
| `4698e4b6` | STUStatescriptActionSetVar | SSAction | 0xa8 | +88 m_index:CV*, +90 m_out_Var:CVDynamic*, +98 m_key:CV*, +a0 m_value:CV* | `0x1608998` 0x8 p0 | 1813 / 215 |
| `4e83bc01` | STUStatescriptActionCriteria | SSAction | 0xa8 | +88 m_identifier:CV*, +90 m_targets:CV*, +98 3c7dbbca:CV*, +a0 e0192e0b:CV* | → STUStatescriptAction | 377 / 119 |
| `eb923a6a` | STUStatescriptActionEffect | SSAction | 0x108 | +88 m_effect:CV*, +90 m_parentEntity:CV*, +98 683afa78:CV*, +a0 a3e96c8e:CV*, +a8 91df0e0c:CV*, +b0 m_offset:CV*, +b8 f2f81c48:CV*, +c0 aa235c8c:CV*, +c8 9a5a92d1:CV*, +d0 99254d76:CV*, +d8 ab132ff4:CV*, +e0 b9ede242:CV*, +e8 b278f01d:CV*, +f0 aee895b1:CV*, +f8 ab1ce067:CV*, +100 38db5a68:enum, +104 m_entityScene:enum | `0x15dcd88` 0x8 p2 | 296 / 116 |
| `8b6a3312` | STUStatescriptActionSendGameMessage | f9f1e339 | 0xb0 | +a8 m_targets:CV* | `0x15db930` 0x8 p2 | 164 / 80 |
| `e4d667e6` | *STUStatescriptActionDebugText* | SSAction | 0x90 | +88 m_value:CV* | `0x15db240` 0x8 p2 | 157 / 56 |
| `04cc5359` | *STUStatescriptActionTrackStat* | SSAction | 0xb8 | +88 m_stat:ref, +98 m_stats:ref[], +a8 m_amount:CV*, +b0 m_targets:CV* | → STUStatescriptAction | 83 / 64 |
| `0cdc2e1a` | *STUStatescriptActionArrayAdd* | SSAction | 0xb0 | +88 m_index:CV*, +90 m_out_Array:CVDynamic*, +98 m_key:CV*, +a0 m_value:CV*, +a8 a7e46ea2:u8 | `0x1608730` 0x8 p0 | 82 / 30 |
| `b6853829` | *STUStatescriptActionStopAbility* | SSAction | 0x88 |  | `0x1617df0` 0x8 p1 | 63 / 45 |
| `eaee2743` | *STUStatescriptActionInstanceEvent* | SSAction | 0xa8 | +88 e4e5d387:ref, +98 e857effa:CV*, +a0 m_targets:CV* | → STUStatescriptAction | 58 / 48 |
| `f9ef59a6` | STUStatescriptActionPlayScript | SSAction | 0xb0 | +88 m_params:a51f99c5*[], +98 m_targets:CV*, +a0 m_script:CV*, +a8 m_stackingBehavior:enum, +ac a7981a94:u8 | `0x15dbb80` 0x8 p2 | 52 / 34 |
| `66b5f581` | *STUStatescriptActionGlobalSound* | SSAction | 0x90 | +88 48663e64:CV* | `0x15dc510` 0x8 p2 | 51 / 9 |
| `5c46d801` | STUStatescriptActionDestroyEntity | SSAction | 0x90 | +88 m_entity:CV* | → STUStatescriptAction | 47 / 34 |
| `5179b279` | *STUStatescriptActionArrayClear* | SSAction | 0x90 | +88 m_out_Array:CVDynamic* | `0x16090c0` 0x8 p0 | 46 / 32 |
| `47db120f` | *STUStatescriptActionBroadcastChat* | SSAction | 0xa8 | +88 15931831:CV*, +90 m_text:CV*, +98 10959009:CV*, +a0 e0066ad1:CV* | `0x15cf470` 0x8 p2 | 44 / 6 |
| `5bea9b4b` |  | SSAction | 0xa8 | +88 m_params:a51f99c5*[], +98 m_condition:CV*, +a0 m_gameMessage:CV* | `0x15dba58` 0x8 p2 | 41 / 21 |
| `4fbcd9de` | *STUStatescriptActionUXToast* | SSAction | 0xc0 | +88 6fa954ac:1e89514b*[], +98 fe10b5f4:CV*, +a0 6e116d52:CV*, +a8 bd7a212d:CV*, +b0 m_queueEntity:CV*, +b8 78a140a3:u8 | `0x15d3408` 0x8 p2 | 29 / 10 |
| `4027e912` |  | f9f1e339 | 0xb0 | +a8 m_targets:CV* | `0x15dbca8` 0x8 p2 | 21 / 10 |
| `a88eb9de` | *STUStatescriptActionArrayRemove* | SSAction | 0xa8 | +88 m_index:CV*, +90 m_out_Array:CVDynamic*, +98 m_key:CV*, +a0 m_value:CV* | `0x16091e8` 0x8 p0 | 20 / 11 |
| `0d5a6dd2` | STUStatescriptActionHack | SSAction | 0x88 |  | → STUStatescriptAction | 19 / 15 |
| `b17638d2` |  | SSAction | 0xa8 | +88 6fa954ac:1e89514b*[], +98 bd7a212d:CV*, +a0 m_queueEntity:CV* | `0x15d3078` 0x8 p2 | 19 / 13 |
| `916290ad` | STUStatescriptActionMovementMod | SSAction | 0x98 | +88 m_ops:SSActionMovementModOp*[] | `0x16185e8` 0x8 p1 | 16 / 12 |
| `4559d3c8` |  | SSAction | 0xa8 | +88 d665ef4d:CV*, +90 4b01f284:CV*, +98 f81bdb9a:CV*, +a0 m_targets:CV* | → STUStatescriptAction | 16 / 14 |
| `047f99e4` | STUStatescriptActionNoOp | SSAction | 0x88 |  | `0x1608e70` 0x8 p0 | 15 / 12 |
| `ece45723` | *STUStatescriptActionBroadcastGameMessage* | f9f1e339 | 0xa8 |  | → STUStatescriptAction | 13 / 5 |
| `0e3689f3` | STUStatescriptActionCreateEntity | SSAction | 0xd0 | +88 m_entityDef:CV*, +90 m_position:CV*, +98 2108984c:CV*, +a0 m_parentEntity:CV*, +a8 9b2c967e:CV*, +b0 m_out_Entity:CVDynamic*, +b8 f01b5f96:CV*, +c0 5de7afe7:CV*, +c8 d4619244:u8, +c9 758a67e0:u8 | `0x15da058` 0x8 p2 | 11 / 4 |
| `65f5405f` |  | SSAction | 0xd8 | +88 a0867f60:ref, +98 8eab28b8:str, +a8 e4e5d387:ref, +b8 m_amount:CV*, +c0 m_targets:CV*, +c8 m_objectiveScore:CV*, +d0 9c771e9e:CV* | → STUStatescriptAction | 11 / 11 |
| `3be5172e` |  | SSAction | 0x98 | +88 eefeec53:CV*, +90 0ab6f7cd:CV* | → STUStatescriptAction | 9 / 1 |
| `980a44d5` |  | SSAction | 0x98 | +88 0ff65715:CV*, +90 6c70bcf5:CV* | `0x15cfa38` 0x8 p2 | 9 / 1 |
| `1bb99c36` | *STUStatescriptActionSpawnPet* | SSAction | 0xd8 | +88 m_entityDefinition:CV*, +90 679af128:CV*, +98 m_position:CV*, +a0 99eeff9f:CV*, +a8 87018f61:CV*, +b0 m_initialVelocity:CV*, +b8 2c6ba8ae:CV*, +c0 9c93dc34:CV*, +c8 m_basedEntity:CV*, +d0 d7c59487:CVDynamic* | → STUStatescriptAction | 8 / 6 |
| `b5741c58` | *STUStatescriptActionSetCollisionPriority* | SSAction | 0x98 | +88 m_targets:CV*, +90 m_priority:CV* | `0x161b3f8` 0x8 p1 | 7 / 3 |
| `dea4f9bc` | *STUStatescriptActionUXDisconnect* | SSAction | 0xa0 | +88 m_fromInstance:CV*, +90 m_fromLobby:CV*, +98 bf8cf4f3:CV* | `0x15d07b8` 0x8 p2 | 7 / 4 |
| `fad70581` | STUStatescriptActionEjectCosmeticEntity | SSAction | 0xa0 | +88 933a1b70:CV*, +90 m_lifeTime:CV*, +98 m_velocityScale:CV* | `0x15ddc50` 0x8 p2 | 6 / 4 |
| `757a8a94` | *STUStatescriptActionSingleRaycast* | SSAction | 0xd8 | +88 m_ignoreEntities:CV*[], +98 m_startPosWS:CV*, +a0 m_dirWS:CV*, +a8 m_castLength:CV*, +b0 m_castFilter:CVFilter*, +b8 m_out_HitSomething:CVDynamic*, +c0 m_out_HitEntity:CVDynamic*, +c8 m_out_HitPointWS:CVDynamic*, +d0 m_out_HitNormal:CVDynamic* | `0x15d3d90` 0x8 p2; `0x15d7410` 0x8 p1 | 6 / 6 |
| `5efae015` |  | SSAction | 0x98 | +88 de466214:CV*, +90 783169a0:CV* | `0x15dd3d0` 0x8 p2 | 6 / 1 |
| `d285a1bb` | *STUStatescriptActionFindEntities* | SSAction | 0xb0 | +88 m_out_Entities:CVDynamic*, +90 bc189043:CV*, +98 a8dcabcd:CV*, +a0 84dd0e91:CV*, +a8 m_filter:CVFilter* | `0x161b2d0` 0x8 p1 | 5 / 4 |
| `8f76a28c` |  | SSAction | 0xa0 | +88 695c8004:CV*, +90 m_target:CV*, +98 m_behavior:enum | `0x15cf598` 0x8 p2 | 5 / 2 |
| `97bf9643` |  | SSAction | 0x88 |  | `0x15ede80` 0x8 p2 | 5 / 4 |
| `2501a4db` | *STUStatescriptActionPickTip* | SSAction | 0xd0 | +88 m_buckets:ref[], +98 4f00bdd2:CV*, +a0 604abce2:CV*, +a8 510e6f9b:CV*, +b0 8c7e1f1f:CV*, +b8 0a32afa1:CV*, +c0 m_out_Text:CV*, +c8 m_out_Priority:CV* | `0x15db490` 0x8 p2 | 4 / 4 |
| `a47f6505` | *STUStatescriptActionSpectateSelf* | SSAction | 0x90 | +88 3acef53b:CV* | `0x15f0590` 0x8 p2 | 4 / 1 |
| `321be080` | STUStatescriptActionSwitch | SSAction | 0xb0 | +88 7d662124:57e931d8*[], +98 b1c64d7f:CV*, +a0 m_switchValue:CV*, +a8 8ae7d668:57e931d8* | `0x1608600` 0x8 p0 | 4 / 2 |
| `de4f0f88` | *STUStatescriptActionTeleportTo* | SSAction | 0xa0 | +88 m_target:CV*, +90 0b4169ac:CV*, +98 91255cca:CV* | → STUStatescriptAction | 4 / 4 |
| `c43f6a1b` | *STUStatescriptActionUXSendHeroSelection* | SSAction | 0xb0 | +88 m_hero:CV*, +90 8530b547:CV*, +98 m_skin:CV*, +a0 aae9ff1a:CV*, +a8 m_behavior:enum | `0x15dae98` 0x8 p2 | 4 / 1 |
| `2ba7bad2` |  | SSAction | 0xa0 | +88 m_name:CV*, +90 m_description:CV*, +98 7ee40f45:enum | `0x15ed9e0` 0x8 p2 | 4 / 1 |
| `17c9f80b` | *STUStatescriptActionCycleIndex* | SSAction | 0xb0 | +88 m_count:CV*, +90 m_condition:CV*, +98 188934cd:CV*, +a0 m_out_Index:CVDynamic*, +a8 aca9b916:CV* | `0x1608f98` 0x8 p0 | 3 / 1 |
| `c370abc0` | STUStatescriptActionPauseAnim | SSAction | 0x98 | +88 m_animID:CV*, +90 76fd4ce8:CV* | `0x15e5738` 0x8 p2 | 3 / 3 |
| `f5a51858` | STUStatescriptActionRepeat | SSAction | 0xa8 | +88 m_count:CV*, +90 m_out_Var:CVDynamic*, +98 m_value:CV*, +a0 7bac6a4e:SSOutputPlug* | `0x16084d8` 0x8 p0 | 3 / 3 |
| `a257245a` | *STUStatescriptActionSetTeamIndex* | SSAction | 0x98 | +88 m_targets:CV*, +90 m_teamIndex:CV* | → STUStatescriptAction | 3 / 2 |
| `1bd4fa79` | *STUStatescriptActionSpectateTeam* | SSAction | 0x98 | +88 21ae0c2d:CV*, +90 ff7c8555:CV* | `0x15f0b60` 0x8 p2 | 3 / 1 |
| `0f4597e7` |  | SSAction | 0x88 |  | `0x15d16d8` 0x8 p2 | 3 / 3 |
| `5046363d` |  | SSAction | 0x98 | +88 m_effects:41cb5614*[] | `0x15d9cc8` 0x8 p2 | 3 / 2 |
| `83ed8ca5` |  | SSAction | 0x88 |  | `0x15cfc88` 0x8 p2 | 3 / 1 |
| `bd6a3dff` | *STUStatescriptActionAddTargetTags* | SSAction | 0x98 | +88 5fb6d29d:CV*, +90 m_targetTags:CV* | → STUStatescriptAction | 2 / 1 |
| `f90c6c08` | *STUStatescriptActionGameFinished* | SSAction | 0x90 | +88 41090ce8:CVDynamic* | → STUStatescriptAction | 2 / 2 |
| `f8ec9531` | STUStatescriptActionRestartAnim | SSAction | 0x90 | +88 m_animID:CV* | `0x15e5860` 0x8 p2 | 2 / 2 |
| `69b029cd` | *STUStatescriptActionUXCancelPurchase* | SSAction | 0x90 | +88 m_guid:CV* | `0x15ee578` 0x8 p2 | 2 / 1 |
| `12213f7e` |  | SSAction | 0xa0 | +88 0ab6f7cd:CV*, +90 6f79ff75:CV*, +98 5dc4ca76:CV* | → STUStatescriptAction | 2 / 1 |
| `2515c992` |  | SSAction | 0x88 |  | `0x15edc30` 0x8 p2 | 2 / 2 |
| `5a6c982d` |  | SSAction | 0x88 |  | `0x15cf6c0` 0x8 p2 | 2 / 1 |
| `80b6f179` |  | SSAction | 0x88 |  | `0x15cfdb0` 0x8 p2 | 2 / 1 |
| `8dfee0a2` |  | SSAction | 0x88 |  | `0x15e80a0` 0x8 p2 | 2 / 1 |
| `9f8f5a55` |  | SSAction | 0xa0 | +88 e5dc4915:CVDynamic*, +90 981fbaec:CV*, +98 852c7046:CV* | → STUStatescriptAction | 2 / 2 |
| `c2e4a7f7` |  | SSAction | 0x88 |  | `0x15e82f0` 0x8 p2 | 2 / 2 |
| `cc7b6828` |  | SSAction | 0x98 | +88 5fbce770:CVDynamic*, +90 53e84aef:enum | → STUStatescriptAction | 2 / 2 |
| `efa122bf` |  | SSAction | 0x88 |  | `0x15edb08` 0x8 p2 | 2 / 1 |
| `4f67103d` | STUStatescriptActionBeamEffect | SSAction | 0xd8 | +88 m_beamEffect:CV*, +90 4667a64e:CV*, +98 1e4a0a7d:CV*, +a0 m_sourceHardpoint:CV*, +a8 f5895fed:CV*, +b0 d18f0a24:CV*, +b8 f769ddb3:CV*, +c0 m_destinationHardpoint:CV*, +c8 m_destinationOffset:CV*, +d0 38db5a68:enum, +d4 8ade9e40:u8 | `0x15dc170` 0x8 p2 | 1 / 1 |
| `a8880f27` | STUStatescriptActionBlink | SSAction | 0xe0 | +88 m_distance:CV*, +90 abce83f7:CV*, +98 f0f1b980:CV*, +a0 fd9c3a45:CV*, +a8 1337c721:CV*, +b0 b69e5d6e:CV*, +b8 b92e282a:CV*, +c0 5785d54e:CV*, +c8 fb0f56ec:CV*, +d0 e9c7bbf7:CV*, +d8 c125509e:CV* | `0x15e67f0` 0x8 p2; `0x15e6df8` 0x8 p1 | 1 / 1 |
| `78184003` | *STUStatescriptActionRemoveTargetTags* | SSAction | 0x98 | +88 5fb6d29d:CV*, +90 m_targetTags:CV* | → STUStatescriptAction | 1 / 1 |
| `4f14c91b` | *STUStatescriptActionRespawnPlayer* | SSAction | 0x90 | +88 18394ef1:CV* | → STUStatescriptAction | 1 / 1 |
| `def847d3` | *STUStatescriptActionResurrect* | SSAction | 0xa0 | +88 5e7eb320:2925214c[], +98 m_targets:CV* | → STUStatescriptAction | 1 / 1 |
| `db1b4c5a` | *STUStatescriptActionSpectateNext* | SSAction | 0x88 |  | `0x15f07e0` 0x8 p2 | 1 / 1 |
| `d462de65` | *STUStatescriptActionSpectatePrevious* | SSAction | 0x88 |  | `0x15f06b8` 0x8 p2 | 1 / 1 |
| `7b838dd2` | *STUStatescriptActionUXConfirmPurchase* | SSAction | 0x90 | +88 m_guid:CV* | `0x15ee450` 0x8 p2 | 1 / 1 |
| `e26e3861` | *STUStatescriptActionUXFriendRequest* | SSAction | 0x90 | +88 13c3db60:CV* | `0x15cf910` 0x8 p2 | 1 / 1 |
| `3fccf8cb` | *STUStatescriptActionUXPrepPurchase* | SSAction | 0x90 | +88 m_guid:CV* | `0x15ee328` 0x8 p2 | 1 / 1 |
| `00435299` |  | SSAction | 0x88 |  | `0x15eff98` 0x8 p2 | 1 / 1 |
| `119cc7b4` |  | SSAction | 0x98 | +88 m_hero:CV*, +90 81c71ffc:CV* | `0x15dac48` 0x8 p2 | 1 / 1 |
| `16b3f570` |  | SSAction | 0x90 | +88 d1241901:CV* | `0x15d0000` 0x8 p2 | 1 / 1 |
| `17fb815b` |  | SSAction | 0x88 |  | `0x15e8540` 0x8 p2 | 1 / 1 |
| `346caded` |  | SSAction | 0x98 | +88 m_hero:CV*, +90 a823b491:CV* | `0x15dad70` 0x8 p2 | 1 / 1 |
| `350e207d` |  | SSAction | 0x98 | +88 eefeec53:CV*, +90 b5477441:CVDynamic* | → STUStatescriptAction | 1 / 1 |
| `3598b46b` |  | f9f1e339 | 0xb0 | +a8 m_targets:CV* | `0x15db808` 0x8 p2 | 1 / 1 |
| `3e114c80` |  | SSAction | 0x98 | +88 4dec3d6e:CV*, +90 36b91630:CV* | `0x15e7d28` 0x8 p2 | 1 / 1 |
| `4b33a20a` |  | SSAction | 0x88 |  | `0x15e8418` 0x8 p2 | 1 / 1 |
| `4f961c19` |  | SSAction | 0xa0 | +88 56000e73:CV*, +90 m_mapGUID:CV*, +98 7e8f85f2:CV* | `0x15ed8b8` 0x8 p2 | 1 / 1 |
| `58419082` |  | SSAction | 0x90 | +88 0cd0c39a:CV* | `0x15cfb60` 0x8 p2 | 1 / 1 |
| `5b25c120` |  | SSAction | 0x90 | +88 m_name:CV* | `0x15edfa8` 0x8 p2 | 1 / 1 |
| `6626064f` |  | SSAction | 0x88 |  | `0x15d0128` 0x8 p2 | 1 / 1 |
| `670cf926` |  | SSAction | 0x98 | +88 f13ba264:CV*, +90 7f0ddbb5:CV* | → STUStatescriptAction | 1 / 1 |
| `6dbdfe7c` |  | SSAction | 0x90 | +88 a684a47a:CV* | → STUStatescriptAction | 1 / 1 |
| `919d3254` |  | SSAction | 0x90 | +88 7e5a0bac:CV* | `0x15e81c8` 0x8 p2 | 1 / 1 |
| `9320731b` |  | SSAction | 0x88 |  | `0x15e7e50` 0x8 p2 | 1 / 1 |
| `b90243cd` |  | SSAction | 0x90 | +88 e15bbc72:CV* | `0x15edd58` 0x8 p2 | 1 / 1 |
| `c078e94e` |  | SSAction | 0x98 | +88 3070b691:CV*, +90 3a98f0ad:CV* | `0x15e7f78` 0x8 p2 | 1 / 1 |
| `ce39353f` |  | f9f1e339 | 0xa8 |  | `0x15db368` 0x8 p2 | 1 / 1 |
| `ceabcd77` |  | SSAction | 0x90 | +88 m_queueEntity:CV* | `0x15d3788` 0x8 p2 | 1 / 1 |
| `d27bef9b` |  | SSAction | 0x90 | +88 ceae80f0:u8 | `0x15db5b8` 0x8 p2 | 1 / 1 |
| `d5c8d941` |  | SSAction | 0x88 |  | → STUStatescriptAction | 1 / 1 |
| `d99fde5d` |  | SSAction | 0x98 | +88 m_forEntity:CV*, +90 4a1c4065:CVDynamic* | → STUStatescriptAction | 1 / 1 |
| `daa09078` |  | SSAction | 0x98 | +88 m_entity:CV*, +90 4b48aa91:CV* | `0x15dbdd0` 0x8 p2 | 1 / 1 |
| `f8b27ec4` |  | SSAction | 0x88 |  | `0x15cfed8` 0x8 p2 | 1 / 1 |
| `2ecefc8b` | STUStatescriptAction | SSActionTerminator | 0x88 | +80 m_outPlug:SSOutputPlug* | `0x15cf220` 0x8 p0 | – |
| `f1201eda` | *STUStatescriptActionEndMatch* | SSAction | 0x88 |  | → STUStatescriptAction | – |
| `1e0f81b5` | *STUStatescriptActionFindNewPath* | SSAction | 0x98 | +88 m_success:SSOutputPlug*, +90 m_failure:SSOutputPlug* | → STUStatescriptAction | – |
| `7d36e679` | *STUStatescriptActionHideEntity* | SSAction | 0x90 | +88 m_entity:CV* | `0x15d9670` 0x8 p2 | – |
| `8194c261` | *STUStatescriptActionResetTips* | SSAction | 0x98 | +88 m_buckets:ref[] | `0x15db6e0` 0x8 p2 | – |
| `394a4156` | *STUStatescriptActionSetVars* | SSAction | 0x98 | +88 2ffd2c32:128dec4f*[] | `0x1608860` 0x8 p0 | – |
| `ac88de21` | *STUStatescriptActionStopPathing* | SSAction | 0x88 |  | → STUStatescriptAction | – |
| `7b628f60` | *STUStatescriptActionUnhideEntity* | SSAction | 0x90 | +88 m_entity:CV* | `0x15d9548` 0x8 p2 | – |
| `128dec4f` |  | SSAction | 0x98 | +88 m_out_Var:CVDynamic*, +90 m_value:CV* | → STUStatescriptAction | – |
| `13903452` |  | SSAction | 0xa0 | +88 e59a5847:CV*, +90 b29b19af:CVDynamic*, +98 m_type:enum | → STUStatescriptAction | – |
| `24c5e1fe` |  | SSAction | 0xa0 | +88 e59a5847:CV*, +90 b29b19af:CVDynamic*, +98 m_type:enum | → STUStatescriptAction | – |
| `585a5b74` |  | SSAction | 0xb0 | +88 11792233:CV*, +90 m_out_Offset:CVDynamic*, +98 m_out_Pitch:CVDynamic*, +a0 m_out_Yaw:CVDynamic*, +a8 m_out_Roll:CVDynamic* | `0x16183d8` 0x8 p1 | – |
| `991f8eb1` |  | SSAction | 0x98 | +88 441190f0:CV*, +90 m_target:CV* | `0x15cf348` 0x8 p2 | – |
| `9d3caa58` |  | SSAction | 0x98 | +88 712bdaf2:str | → STUStatescriptAction | – |
| `b276bc58` |  | SSAction | 0xa0 | +88 m_stat:ref, +98 m_targets:CV* | → STUStatescriptAction | – |
| `cd93896e` |  | SSAction | 0x90 | +88 5fb6d29d:CV* | `0x161b080` 0x8 p1 | – |
| `e0b5f28f` |  | SSAction | 0x98 | +88 m_hero:CV*, +90 m_out_Array:CVDynamic* | `0x161b1a8` 0x8 p1 | – |
| `f9f1e339` |  | SSAction | 0xa8 | +88 m_params:a51f99c5*[], +98 m_gameMessage:CV*, +a0 694d2b8b:CV* | → STUStatescriptAction | – |
| `fd644ab8` |  | SSAction | 0x88 |  | `0x15cf7e8` 0x8 p2 | – |

## Action terminators (no output plug) (3)

| hash | name | parent | STU | own fields | runtime | uses |
|---|---|---|---|---|---|---|
| `85d2552d` | *STUStatescriptActionStopScript* | SSActionTerminator | 0x80 |  | `0x1608d48` 0x8 p0 | 31 / 24 |
| `dc0f60f6` | *STUStatescriptActionResetScript* | SSActionTerminator | 0x88 | +80 652a6221:u8 | `0x1608c20` 0x8 p0 | – |
| `15b44727` | STUStatescriptActionTerminator | SSBase | 0x80 | +78 m_inPlug:SSInputPlug* | – | – |

## States (259)

| hash | name | parent | STU | own fields | runtime | uses |
|---|---|---|---|---|---|---|
| `f7d6498c` | STUStatescriptStateStack | SSState | 0x128 | +100 m_out_Var:CVDynamic*, +108 m_priority:CV*, +110 m_value:CV*, +118 m_topSubgraphPlug:24ab1f0b*, +120 m_underSubgraphPlug:24ab1f0b* | `0x15c8b60` 0x108 p2; `0x15c9ed8` 0xe0 p0 | 823 / 182 |
| `e811ecbb` | STUStatescriptStateBooleanSwitch | SSState | 0x128 | +100 m_condition:CV*, +108 m_onTruePlug:SSOutputPlug*, +110 m_onFalsePlug:SSOutputPlug*, +118 m_trueSubgraphPlug:24ab1f0b*, +120 m_falseSubgraphPlug:24ab1f0b* | `0x1605058` 0x140 p0 | 782 / 190 |
| `65f158da` | STUStatescriptStateWait | SSState | 0x120 | +100 m_timeout:CV*, +108 m_onAbortPlug:SSOutputPlug*, +110 m_onFinishedPlug:SSOutputPlug*, +118 1bff3867:u8, +119 c5a427fa:u8 | `0x16052b0` 0xb8 p0 | 708 / 189 |
| `970ec198` | STUStatescriptStateChaseVar | SSState | 0x160 | +100 m_destination:CV*, +108 m_destinationBone:CV*, +110 m_destinationObject:CV*, +118 3746cf3c:CV*, +120 eb254e1e:CV*, +128 fc7bc05c:CV*, +130 m_out_Var:CVDynamic*, +138 41430518:CV*, +140 924cebfa:CV*, +148 m_onAbortPlug:SSOutputPlug*, +150 m_onFinishedPlug:SSOutputPlug*, +158 9f6b675b:u8, +159 b36f935f:u8 | `0x15c8e30` 0x180 p2; `0x15c8698` 0x180 p1; `0x15c9a18` 0x180 p0 | 263 / 96 |
| `7fb4bf64` | STUStatescriptStateAnim | SSState | 0x168 | +100 f9045e7a:CV*, +108 m_animCategory:CV*, +110 6d640a54:CV*, +118 m_speedScalar:CV*, +120 4d475080:CV*, +128 bfd49faf:CV*, +130 a7fbdeef:CV*, +138 5c5addbe:CVDynamic*, +140 m_startTime:CV*, +148 m_endTime:CV*, +150 m_priority:CV*, +158 m_onFinishedPlug:SSOutputPlug*, +160 dc1c7eb2:enum, +164 41e7159c:u8, +165 e9733704:u8 | `0x15e59a0` 0x1a0 p2; `0x15e54d0` 0x110 p1 | 259 / 108 |
| `85c28a9a` | STUStatescriptStateDataFlowMapping | SSState | 0x118 | +100 m_dataFlow:CV*, +108 m_answer:CV*, +110 m_entity:SSDataFlowEntity* | `0x15dd9c8` 0xd8 p2 | 240 / 84 |
| `ffbfa88d` | STUStatescriptStateEffect | SSState | 0x190 | +100 m_effect:CV*, +108 m_parentEntity:CV*, +110 683afa78:CV*, +118 a3e96c8e:CV*, +120 91df0e0c:CV*, +128 m_offset:CV*, +130 f2f81c48:CV*, +138 aa235c8c:CV*, +140 9a5a92d1:CV*, +148 99254d76:CV*, +150 ab132ff4:CV*, +158 b278f01d:CV*, +160 aee895b1:CV*, +168 142abd7e:CV*, +170 c2cebb43:CV*, +178 ab1ce067:CV*, +180 2f7d78d4:CV*, +188 m_entityScene:enum | `0x15dc8b8` 0x170 p2 | 207 / 106 |
| `ff77458c` | STUStatescriptStateLogicalButton | SSState | 0x140 | +100 m_logicalButton:CV*, +108 0338e8e4:CV*, +110 8cbe9822:CV*, +118 189b3875:CV*, +120 m_onGoingDownPlug:SSOutputPlug*, +128 m_onComingUpPlug:SSOutputPlug*, +130 8496114b:24ab1f0b*, +138 29909c7c:24ab1f0b* | `0x15c7f70` 0x170 p2; `0x15ca160` 0xc8 p0 | 175 / 72 |
| `c2a01675` | *STUStatescriptStateUXViewModel* | SSStateUXBase | 0x188 | +150 6c43bd5f:CV*, +158 738d8998:CV*, +160 ccde96d8:CV*, +168 5376f269:CV*, +170 83ccec5b:CVFilter*, +178 8cc04a8e:CVFilter*, +180 m_priority:CV* | `0x15cccc0` 0x400 p2 | 158 / 108 |
| `6e81f6d4` | STUStatescriptStateCosmeticEntity | SSState | 0x1e0 | +100 e971f3fd:STUCreateArmIkItem[], +110 db47917f:STUCreateArmIkItem[], +120 m_entityDef:CV*, +128 68fd8535:CV*, +130 m_skin:CV*, +138 c2cebb43:CV*, +140 aa235c8c:CV*, +148 9a5a92d1:CV*, +150 f2f81c48:CV*, +158 23435668:CV*, +160 5f030948:CV*, +168 m_offset:CV*, +170 m_parentEntity:CV*, +178 683afa78:CV*, +180 a3e96c8e:CV*, +188 91df0e0c:CV*, +190 ddabfc50:CV*, +198 7bc3ecfc:CV*, +1a0 23965996:CV*, +1a8 7cf5ba2a:CV*, +1b0 99254d76:CV*, +1b8 ab132ff4:CV*, +1c0 m_out_Handle:CVDynamic*, +1c8 142abd7e:CV*, +1d0 m_entityScene:enum, +1d4 m_resPriority:enum, +1d8 01275138:enum, +1dc d8d97d49:u8, +1dd 79eba050:u8 | `0x15ddfe0` 0xd0 p2; `0x15de260` 0xb8 p1 | 140 / 66 |
| `a7213568` | STUStatescriptState | SSBase | 0x100 | +78 m_stateGroup:ref, +88 m_transitionPlug:8f6c88d9*[], +98 065d841c:i32[], +a8 652a186c:CVDynamic*[], +b8 3882159e:CVDynamic*[], +c8 a2eca5de:SSSubGraph*, +d0 m_beginPlug:SSInputPlug*, +d8 m_abortPlug:e51f3e8d*, +e0 m_onBeginPlug:f1f0a36d*, +e8 m_onEndPlug:SSOutputPlug*, +f0 m_subgraphPlug:24ab1f0b*, +f8 63d0b555:i32, +fc 36391723:u32 | `0x15ca730` 0xb8 p0 | 124 / 77 |
| `498b421a` | STUStatescriptStateTrackTargets | SSState | 0x1b0 | +100 7399b635:CV*, +108 9d812041:CV*, +110 f6b861b5:CV*, +118 bf690574:CV*, +120 m_posEntity:CV*, +128 m_posHardpoint:CV*, +130 m_posOffset:CV*, +138 f0813d54:CV*, +140 m_radius:CV*, +148 9976e589:CV*, +150 f8c29d41:CV*, +158 cbc0937e:CV*, +160 08f9cb86:CVFilter*, +168 88d85e76:CV*, +170 ff7a1a83:CV*, +178 m_out_Targets:CVDynamic*, +180 a6b67acb:CVDynamic*, +188 3afd9b25:02d49f9a*, +190 m_filter:CVFilter*, +198 21dfe0d6:CV*, +1a0 5e4a73de:enum, +1a4 8004b1bd:enum, +1a8 3cc558e5:u8, +1a9 4922b492:u8, +1aa 854cd6b8:u8, +1ab 12797087:u8, +1ac 1561d3d4:u8, +1ad 7affe568:u8, +1ae c7293391:u8 | `0x15d4d50` 0x2c0 p2; `0x15d9048` 0x2c0 p1 | 109 / 57 |
| `02e4a0ab` | STUStatescriptStateMovementMod | SSState | 0x118 | +100 m_ops:SSStateMovementModOp*[], +110 142abd7e:CV* | `0x1618968` 0xb8 p1 | 86 / 58 |
| `619d9e95` | STUStatescriptStateModifyHealth | SSState | 0x150 | +100 d525b22b:CV*, +108 m_modifyHealth:SSModifyHealth*, +110 ebda16f6:CV*, +118 9cc9f017:CV*, +120 6cb57812:CV*, +128 b70d8ddf:CV*, +130 c264695e:CV*, +138 m_onAbortPlug:SSOutputPlug*, +140 m_onFinishedPlug:SSOutputPlug*, +148 8004b1bd:enum, +14c 83377b7d:u8, +14d 73abfb05:u8 | → STUStatescriptState | 81 / 52 |
| `3db862c4` | STUStatescriptStateSubScript | SSStateSubScriptBase | 0x130 | +128 m_statescriptGraph:CV* | `0x1621978` 0xc0 p1 | 77 / 21 |
| `860ee5fc` | STUStatescriptStateWeaponVolley | SSState | 0x210 | +100 m_projectileMotions:SSWeaponProjectileMotion*[], +110 m_projectileEntity:SSWeaponProjectileEntity*, +118 m_projectileVisual:SSWeaponProjectileVisual*, +120 m_aimID:CV*, +128 m_projectileLifetime:CV*, +130 308e35d5:CV*, +138 4f7ebb93:SSWeaponProjectileRicochet*, +140 m_projectileSpeed:CV*, +148 6a53f8c7:SSWeaponProjectileSticky*, +150 d789b6ca:SSWeaponProjectileSettle*, +158 m_raycastType:CV*, +160 5b97fa06:SSWeaponProjectileExplosion*, +168 3d131607:SSWeaponProjectileImpact*, +170 20958464:db6f4126*, +178 3307a07b:SSWeaponProjectileSpawn*, +180 3ac99059:07e34e24*, +188 a4e081c9:CV*, +190 m_numProjectilesPerShot:CV*, +198 m_numShotsPerSecond:CV*, +1a0 64389377:CV*, +1a8 a835e878:CV*, +1b0 8bc9d90b:312e3316*, +1b8 764a8ec7:f9ae352d*, +1c0 203488d6:CV*, +1c8 80ad81f5:SSWeaponVolleyLaunch*, +1d0 m_out_Ammo:CVDynamic*, +1d8 a2a6d6a4:CVDynamic*, +1e0 663ce7e5:CVDynamic*, +1e8 2ccaffb8:CVDynamic*, +1f0 m_onMinShotsPlug:SSOutputPlug*, +1f8 m_onAbortPlug:SSOutputPlug*, +200 m_onFinishedPlug:SSOutputPlug*, +208 b3276522:enum, +20c bc36e891:u8 | `0x15d2450` 0x340 p2 | 65 / 41 |
| `f53c5da8` | STUStatescriptStateAbility | SSState | 0x208 | +100 e018f16a:SSAbilityVarToSet[], +110 b2ff48fa:SSAbilityVarToSet[], +120 2d76e984:SSAbilityVarToSet[], +130 37f2b53c:SSAbilityVarToSet[], +140 40f679b6:SSAbilityVarToSet[], +150 0a3410bc:CV*, +158 b6cfe3e2:CV*, +160 0338e8e4:CV*, +168 14fd5806:CV*, +170 m_startButton:CV*, +178 f838418b:CV*, +180 3e22a5bc:CV*, +188 522e454a:CV*, +190 4c5aa514:CV*, +198 25198346:CV*, +1a0 m_out_CooldownVar:CVDynamic*, +1a8 84c50e4b:CV*, +1b0 m_startCondition:CV*, +1b8 543a9538:CV*, +1c0 db1971cb:CV*, +1c8 m_out_StackVar:CVDynamic*, +1d0 752471c2:CV*, +1d8 3df852d3:CV*, +1e0 92c13ac6:CV*, +1e8 170fc668:8dcb62ea*, +1f0 42a043f1:24ab1f0b*, +1f8 a6e2adb2:SSOutputPlug*, +200 211de955:SSOutputPlug* | `0x15d7c98` 0x210 p2; `0x15d81b0` 0x1f0 p1 | 63 / 59 |
| `9f74c07c` | *STUStatescriptStateClientonlyController* | SSState | 0x108 | +100 0f944cc0:24ab1f0b* | `0x15c9310` 0x150 p2 | 58 / 35 |
| `4ab09851` |  | SSState | 0x110 | +100 5cd2d4aa:CVDynamic*[] | → STUStatescriptState | 57 / 57 |
| `0459c53d` | STUStatescriptStateUIInWorld | SSStateUXBase | 0x1d8 | +150 m_regions:34493776*[], +160 m_filter:CVFilter*, +168 d1d8c2e6:CV*, +170 802179bb:CV*, +178 m_minScale:CV*, +180 m_maxScale:CV*, +188 ef1c0e48:CV*, +190 m_posOffset:CV*, +198 95a1a656:CV*, +1a0 e2a696c0:CV*, +1a8 1cb0801b:CV*, +1b0 d469bb7f:CV*, +1b8 7ed72b91:CV*, +1c0 5ddc3711:CV*, +1c8 cca87998:CV*, +1d0 978d2da1:enum, +1d4 68cef10f:enum | `0x15cd028` 0x440 p2 | 53 / 33 |
| `c739baac` | STUStatescriptStateUXScreen | SSStateUXBase | 0x150 |  | `0x15ce2a8` 0x3e0 p2 | 50 / 37 |
| `a97b875f` | *STUStatescriptStatePopulateTips* | SSState | 0x110 | +100 m_tips:STUTip[] | `0x15c7a80` 0x150 p2 | 44 / 27 |
| `e67578c0` | STUStatescriptStateWatch | SSState | 0x118 | +100 m_watchedVal:CV*, +108 b0f08764:CVDynamic*, +110 a415be52:SSOutputPlug* | `0x1605508` 0x120 p0 | 38 / 32 |
| `26367267` | STUStatescriptStateSwitch | SSState | 0x128 | +100 3fd0156c:136270d8*[], +110 cdfceefc:CV*, +118 m_switchValue:CV*, +120 f3c7685f:24ab1f0b* | `0x16059b8` 0xc8 p0 | 35 / 24 |
| `d78490d1` | STUStatescriptStateApplyAura | SSState | 0x128 | +100 m_params:a51f99c5*[], +110 m_aura:CV*, +118 m_targets:CV*, +120 b9607afa:CV* | → STUStatescriptState | 34 / 25 |
| `01d6952a` | STUStatescriptStateLimitAim | SSState | 0x140 | +100 32828485:CV*, +108 1ec39a9a:CV*, +110 1a59abb0:CV*, +118 64eba466:CV*, +120 73a96984:CV*, +128 60ec8f2f:CV*, +130 15e0d656:CVDynamic*, +138 m_behavior:enum | `0x1618180` 0x100 p1 | 34 / 26 |
| `6bfa64af` | STUStatescriptStateUXReticleOverride | SSState | 0x160 | +100 36aab4ed:CV*, +108 29a4e1a7:CV*, +110 6cb9097b:CV*, +118 7b32776e:CV*, +120 62030719:CV*, +128 4d32b2a1:CV*, +130 828e23de:CV*, +138 e1148ba3:CV*, +140 ec8d000c:CV*, +148 2c5c818b:CV*, +150 37ea5ad9:CV*, +158 m_reticleType:enum | `0x15cca48` 0xc8 p2; `0x15ce8c0` 0xb8 p1 | 30 / 23 |
| `c0a88bb0` | STUStatescriptStateBeamEffect | SSState | 0x158 | +100 m_beamEffect:CV*, +108 4667a64e:CV*, +110 1e4a0a7d:CV*, +118 m_sourceHardpoint:CV*, +120 f5895fed:CV*, +128 d18f0a24:CV*, +130 f769ddb3:CV*, +138 m_destinationHardpoint:CV*, +140 m_destinationOffset:CV*, +148 c2cebb43:CV*, +150 8ade9e40:u8 | `0x15dcf10` 0x190 p2 | 29 / 15 |
| `cd396790` |  | SSState | 0x140 | +100 6b7c7d86:CV*, +108 b4fe98d8:CV*, +110 9d9d030f:CVDynamic*, +118 69afa3be:CVDynamic*, +120 3ecdfc9f:CVDynamic*, +128 m_onAbortPlug:SSOutputPlug*, +130 m_onFinishedPlug:SSOutputPlug*, +138 47e1781c:24ab1f0b* | `0x15d4840` 0xb8 p2; `0x15d86c8` 0xb8 p1 | 29 / 16 |
| `a7ca1073` | *STUStatescriptStateTrackStatTime* | SSState | 0x118 | +100 m_stat:ref, +110 m_targets:CV* | → STUStatescriptState | 22 / 12 |
| `a5f89e3f` | STUStatescriptStateCameraTransform | SSState | 0x110 | +100 072cbdd4:CV*, +108 m_cameraType:enum, +10c 1dfe3f77:enum | `0x15f16a0` 0xe0 p2 | 20 / 14 |
| `c17b255d` | STUStatescriptStateHealthPool | SSState | 0x150 | +100 ebda16f6:CV*, +108 1bb6c936:CV*, +110 m_amount:CV*, +118 fe8b4208:CV*, +120 9d2be4ac:CV*, +128 efd1f791:CVDynamic*, +130 d38fd608:CVDynamic*, +138 m_isIntrinsic:CV*, +140 174729a4:CV*, +148 36967fde:enum | → STUStatescriptState | 20 / 17 |
| `7331cb24` | STUStatescriptStateInvisible | SSState | 0x118 | +100 5c2ebbd3:CV*, +108 13313c89:CV*, +110 a2867f52:CV* | `0x15d45e8` 0xe0 p2 | 20 / 14 |
| `7b3ed271` | STUStatescriptStateCameraLookAt | SSState | 0x140 | +100 b0c994d6:CV*, +108 2c419df5:CV*, +110 df5df87e:CV*, +118 3fda116d:CV*, +120 142abd7e:CV*, +128 fc6e7571:CV*, +130 b785b622:CV*, +138 8996754a:CV* | `0x15f0f20` 0x180 p2 | 19 / 8 |
| `44e7f474` | STUStatescriptStateGamePadVibration | SSState | 0x138 | +100 m_vibration:CV*, +108 m_priority:CV*, +110 6d640a54:CV*, +118 e9617571:CV*, +120 m_timeScalar:CV*, +128 m_onAbortPlug:SSOutputPlug*, +130 m_onFinishedPlug:SSOutputPlug* | `0x15d97c0` 0x170 p2; `0x15d71b0` 0xc8 p1 | 18 / 7 |
| `323aabcf` | STUStatescriptStateTrackRay | SSState | 0x148 | +100 3324050b:8ef1c54b*, +108 m_sourceEntity:CV*, +110 m_sourceHardpoint:CV*, +118 f5895fed:CV*, +120 f2bddbc9:CV*, +128 m_filter:CVFilter*, +130 5c97bd30:CV*, +138 m_target:b129bd42*, +140 7affe568:u8, +141 c7293391:u8 | `0x15d5718` 0x260 p2; `0x15d67f0` 0xc0 p1 | 17 / 6 |
| `d7ca9c01` | STUStatescriptStateTargetEffect | SSState | 0x140 | +100 7d781ce6:CV*, +108 m_contactSet:CV*, +110 ff36d7ce:CV*, +118 d6be0b2b:CV*, +120 43d94a7d:CV*, +128 m_owner:CV*, +130 142abd7e:CV*, +138 e7b22ecb:CVDynamic* | `0x15d54b0` 0x180 p2 | 16 / 9 |
| `225ae956` |  | SSState | 0x130 | +100 m_position:CV*, +108 m_radius:CV*, +110 m_direction:CV*, +118 m_angle:CV*, +120 m_threatPerSecond:CV*, +128 m_fixedThreat:CV* | `0x15d3b18` 0x160 p2 | 15 / 14 |
| `28d0a23b` | STUStatescriptStateCamera3P | SSState | 0x198 | +100 5de09754:CV*, +108 e4e365d4:CV*, +110 m_distance:CV*, +118 af6345e9:CV*, +120 27a3a371:CV*, +128 m_combatShields:CV*, +130 6afdcf16:CV*, +138 44531843:CV*, +140 d665b452:CV*, +148 31c3f3a6:CV*, +150 426cf7f8:CV*, +158 dcefda76:CV*, +160 f2410d23:CV*, +168 9ceefbd0:CV*, +170 de151fe5:CV*, +178 m_viewOverride:CV*, +180 1653e7e0:CV*, +188 4b0856a5:CV*, +190 5f3548d8:CV* | `0x15dafc8` 0x160 p2 | 13 / 11 |
| `b1001dcf` | STUStatescriptStatePhaseOut | SSState | 0x100 |  | `0x15ced70` 0xe0 p2 | 13 / 12 |
| `2a0fb7e6` | STUStatescriptStateInvincible | SSState | 0x108 | +100 m_target:CV* | → STUStatescriptState | 12 / 10 |
| `8de7e50f` | STUStatescriptStateTrackMovement | SSState | 0x168 | +100 m_entity:CV*, +108 6523e4f2:CVDynamic*, +110 979ab3cb:CVDynamic*, +118 e27094e0:CVDynamic*, +120 8134e047:CVDynamic*, +128 df4af27f:CVDynamic*, +130 7bb19ae0:CVDynamic*, +138 761caf1e:CVDynamic*, +140 964174f9:CVDynamic*, +148 68b1ec07:CVDynamic*, +150 fe68fbaa:CVDynamic*, +158 000946a5:SSOutputPlug*, +160 5b60e813:SSOutputPlug* | `0x1618710` 0x140 p1 | 12 / 12 |
| `381d6654` |  | SSState | 0x110 | +100 m_effects:41cb5614*[] | `0x15d9a50` 0x180 p2 | 12 / 3 |
| `971645fe` | STUStatescriptStateAttach | f1c8d756 | 0x128 | +108 m_parentEntity:CV*, +110 866a2ff5:CV*, +118 a0b01071:CV*, +120 018eb32a:CV* | `0x161a148` 0xb8 p1 | 11 / 10 |
| `d689c470` | *STUStatescriptStateLogicalButtonClientOnly* | SSState | 0x128 | +100 m_logicalButton:CV*, +108 m_onGoingDownPlug:SSOutputPlug*, +110 m_onComingUpPlug:SSOutputPlug*, +118 8496114b:24ab1f0b*, +120 29909c7c:24ab1f0b* | `0x15c7cf8` 0x160 p2 | 11 / 3 |
| `c1035274` | STUStatescriptStateMoveTo | f1c8d756 | 0x1b0 | +108 m_destinationPos:CV*, +110 m_destinationNorm:CV*, +118 m_destinationEntity:CV*, +120 m_destinationBone:CV*, +128 72cd9d3f:CV*, +130 91326f2e:CV*, +138 c53866b0:CV*, +140 2fd01510:CV*, +148 m_speed:CV*, +150 924cebfa:CV*, +158 110b8981:CV*, +160 d39cb643:CV*, +168 7779deeb:CV*, +170 3d6bf338:CV*, +178 e3384b7b:CV*, +180 5cb9ed69:CV*, +188 0205da7b:CV*, +190 5bd8e8e6:CV*, +198 m_onAbortPlug:SSOutputPlug*, +1a0 m_onFinishedPlug:SSOutputPlug*, +1a8 m_serverOnly:u8 | `0x1618e28` 0x110 p1 | 10 / 9 |
| `e19fda31` |  | SSState | 0x110 | +100 m_values:STUGlobalDataFlowValue[] | `0x15c9568` 0xd0 p2 | 10 / 2 |
| `445437ed` |  | SSState | 0x128 | +100 m_forWeapon:ref, +110 m_onEntity:CV*, +118 41b5e36c:CV*, +120 c97f2482:u32 | → STUStatescriptState | 9 / 3 |
| `b0054407` |  | SSState | 0x118 | +100 3276dbb7:CV*, +108 e90fab81:CV*, +110 276f09a7:CV* | `0x15dd4f8` 0xc8 p2 | 8 / 8 |
| `b0d4b2c2` |  | SSState | 0x108 | +100 m_onEntity:CV* | → STUStatescriptState | 8 / 3 |
| `68cb1df9` | STUStatescriptStateCombatModFilter | SSState | 0x150 | +100 d98ecd58:ref[], +110 m_combatMod:fileref, +120 50d7874c:CV*, +128 m_filter:CVFilter*, +130 f1109f2b:CVDynamic*, +138 95347212:CVDynamic*, +140 2a7ee15d:SSOutputPlug*, +148 475005d5:enum | → STUStatescriptState | 7 / 7 |
| `4cced352` | STUStatescriptStatePet | SSState | 0x150 | +100 m_entityDefinition:CV*, +108 679af128:CV*, +110 m_position:CV*, +118 99eeff9f:CV*, +120 87018f61:CV*, +128 m_initialVelocity:CV*, +130 2c6ba8ae:CV*, +138 9c93dc34:CV*, +140 m_basedEntity:CV*, +148 d7c59487:CVDynamic* | → STUStatescriptState | 7 / 7 |
| `160c4f1d` | *STUStatescriptStateValidatePlacement* | SSState | 0x230 | +100 m_ignoreEntities:CV*[], +110 f9d684e3:4e383817*, +118 69bcd8e4:CV*, +120 ad659f53:CV*, +128 9686ba14:CV*, +130 059631a6:CV*, +138 076f6ad9:CV*, +140 6c2b2423:CV*, +148 d2ca83f7:CV*, +150 12588e79:CVFilter*, +158 118ebc19:CV*, +160 8d33c84e:CVFilter*, +168 0bc2f547:CV*, +170 6cd3e4cc:CV*, +178 ce5a9fd2:CV*, +180 fc05c1b1:CV*, +188 7e9df78c:CV*, +190 b0ac7cd9:CV*, +198 6a0ba531:CVDynamic*, +1a0 405e37f3:CVDynamic*, +1a8 0ec430de:CVDynamic*, +1b0 5829096c:CVDynamic*, +1b8 ca08b9eb:CVDynamic*, +1c0 bb79211d:CVDynamic*, +1c8 93ede17d:CVDynamic*, +1d0 c500d8cf:CVDynamic*, +1d8 57216848:CVDynamic*, +1e0 2650f0be:CVDynamic*, +1e8 fbd471a2:CVDynamic*, +1f0 0ce5c131:CV*, +1f8 44aad87e:CV*, +200 dba042d9:CVFilter*, +208 9c480ab9:CV*, +210 m_castFilter:CVFilter*, +218 c63fd388:8dcb62ea*, +220 02701bf5:SSOutputPlug*, +228 d3894419:SSOutputPlug* | `0x15d5e68` 0x130 p2; `0x15d6f40` 0x120 p1 | 7 / 5 |
| `2168e38f` |  | SSStateUXViewModel | 0x1a8 | +188 a5e0221b:CV*, +190 73c8d015:CV*, +198 m_isHostile:CV*, +1a0 ba4297d8:CV* | `0x15d1b20` 0x460 p2 | 7 / 5 |
| `e831bef1` | STUStatescriptStateAssist | SSState | 0x130 | +100 2288ff2a:SSModifyHealthTag[], +110 b2eb93a2:CV*, +118 m_targets:CV*, +120 3ede29dd:CV*, +128 83e06352:CV* | → STUStatescriptState | 6 / 5 |
| `72509d3b` | STUStatescriptStateFindGround | SSState | 0x198 | +100 9a376d58:CV*, +108 3f302ed8:CV*, +110 f91d4382:CV*, +118 8bfe24fe:CV*, +120 f0ae9891:CV*, +128 6ad13eea:CV*, +130 fea20571:CV*, +138 c2af3a28:CV*, +140 e4923200:CV*, +148 cc09f961:CV*, +150 m_castFilter:CVFilter*, +158 c63fd388:8dcb62ea*, +160 02701bf5:SSOutputPlug*, +168 d3894419:SSOutputPlug*, +170 b24ce8d7:CVDynamic*, +178 db2b379d:CVDynamic*, +180 8cfa150e:CVDynamic*, +188 11d623b7:CVDynamic*, +190 bb614c91:CVDynamic* | `0x15d60d8` 0xe0 p2; `0x15d6598` 0xb8 p1 | 6 / 3 |
| `01419d1c` | STUStatescriptStateUXFade | SSStateUXBase | 0x188 | +150 96ab797c:e2eb6a5d*, +158 77679385:SSOutputPlug*, +160 f1222348:CV*, +168 7cd41f33:CV*, +170 8c1f91a9:CV*, +178 m_color:CV*, +180 4669dd92:u8 | `0x15d0e50` 0x400 p2 | 6 / 5 |
| `12db64da` |  | SSState | 0x110 | +100 m_target:CV*, +108 8d59b09d:CV* | `0x1616aa8` 0xb8 p1 | 6 / 3 |
| `1ab3b4d7` | *STUStatescriptStateApplyTargetTags* | SSState | 0x110 | +100 5fb6d29d:CV*, +108 m_targetTags:CV* | → STUStatescriptState | 5 / 5 |
| `f6e1e486` | *STUStatescriptStateLedgeClimb* | f1c8d756 | 0x158 | +108 4003d756:CV*, +110 e1ca2901:CV*, +118 9589dd61:CV*, +120 86002751:CV*, +128 ba0d1808:CV*, +130 9f5ff128:CV*, +138 m_climbSpeed:CV*, +140 cb6dfc75:CVDynamic*, +148 407e184c:CVDynamic*, +150 68d5e130:CVDynamic* | `0x1619090` 0xb8 p1 | 5 / 5 |
| `71c68397` | STUStatescriptStateUXToast | SSState | 0x130 | +100 6fa954ac:1e89514b*[], +110 fe10b5f4:CV*, +118 6e116d52:CV*, +120 bd7a212d:CV*, +128 m_queueEntity:CV* | `0x15d3530` 0xb8 p2 | 5 / 1 |
| `3f27886c` | *STUStatescriptStateWorldFade* | SSState | 0x138 | +100 96ab797c:e2eb6a5d*, +108 f1222348:CV*, +110 7cd41f33:CV*, +118 m_color:CV*, +120 m_onAbortPlug:SSOutputPlug*, +128 m_onFinishedPlug:SSOutputPlug*, +130 c989ffda:enum, +134 4669dd92:u8 | `0x15f2278` 0xc0 p2 | 5 / 1 |
| `614c3a0a` |  | SSState | 0x150 | +100 m_position:CV*, +108 m_direction:CV*, +110 m_width:CV*, +118 45561961:CV*, +120 92bdab20:CV*, +128 m_castFilter:CVFilter*, +130 5556c513:8dcb62ea*, +138 8cf01b33:SSOutputPlug*, +140 3d1f6fbf:CVDynamic*, +148 7d5017c9:CVDynamic* | `0x15d59b8` 0xc8 p2; `0x15d6a70` 0xc0 p1 | 5 / 1 |
| `67471c66` | STUStatescriptStateDeflectProjectiles | SSState | 0x1a8 | +100 m_boxes:SSDeflectProjectileBox[], +110 08f9cb86:CVFilter*, +118 00454cc6:CV*, +120 3c48739f:CV*, +128 a8a412c8:CV*, +130 m_deflectorEntity:CV*, +138 m_filter:CVFilter*, +140 8d177dba:CV*, +148 cf120a8f:CV*, +150 a794a473:CVDynamic*, +158 16e7e1c6:496bf68c*, +160 70af54e6:CV*, +168 ef073057:CV*, +170 56532d66:CV*, +178 9d927eae:CV*, +180 a19f41f7:CV*, +188 d05adfa5:CV*, +190 ec57e0fc:CV*, +198 ecdc6fd3:SSOutputPlug*, +1a0 4de15273:u8, +1a1 13cbba73:u8, +1a2 fcb972e3:u8, +1a3 a6260fe4:u8, +1a4 94106d66:u8 | `0x15d2bf8` 0x1f0 p2; `0x15d2700` 0x130 p1 | 4 / 2 |
| `9c27cbce` | *STUStatescriptStateSuppressCapture* | SSState | 0x108 | +100 a3bb04af:CV* | → STUStatescriptState | 4 / 4 |
| `5b2bb8b6` | *STUStatescriptStateUXPurchaseScreen* | SSStateUXBase | 0x158 | +150 m_guid:CV* | `0x15ebc10` 0x3f0 p2 | 4 / 1 |
| `f45a1956` | STUStatescriptStateWallMove | f1c8d756 | 0x170 | +108 846a3348:STUWallMoveProfile[], +118 9589dd61:CV*, +120 e1ca2901:CV*, +128 bfe84286:CV*, +130 30691eac:CV*, +138 2a61166c:CV*, +140 1e832bb2:CV*, +148 46b246b4:CV*, +150 e9025818:CV*, +158 5bbbcc02:CV*, +160 796cbb06:CV*, +168 94cc601d:CVDynamic* | `0x15e6588` 0xb8 p2; `0x15e6b80` 0xb8 p1 | 4 / 4 |
| `6c4f68a0` |  | 57f3b2c4 | 0x108 |  | → STUStatescriptState | 4 / 4 |
| `c37e44d7` |  | 09fb0449 | 0x120 | +108 79501b77:CV*, +110 5ed70d3c:CV*, +118 3e3ccf0c:CV* | → STUStatescriptState | 4 / 3 |
| `49da8988` | *STUStatescriptStateAimRotate* | SSStateAimBase | 0x148 | +138 ced2cf74:CV*, +140 a0ece9e4:CV* | → STUStatescriptState | 3 / 3 |
| `c7409708` | *STUStatescriptStateAlliesTargetFilter* | SSStateTargetFilterBase | 0x110 | +108 c3fc0311:CV* | → STUStatescriptState | 3 / 3 |
| `1a81cfb7` | STUStatescriptStateAnimatedCamera | SSState | 0x138 | +100 20623cab:CV*, +108 c99c08f2:CV*, +110 cef2bbff:CV*, +118 m_modelEntity:CV*, +120 0cbb8745:CV*, +128 ea9a939d:STUAnimCurve*, +130 342c9279:STUAnimCurve* | `0x15f18f8` 0x160 p2 | 3 / 2 |
| `e7c6aaf6` | STUStatescriptStateCreateEntity | SSState | 0x148 | +100 m_entityDef:CV*, +108 m_position:CV*, +110 2108984c:CV*, +118 m_parentEntity:CV*, +120 9b2c967e:CV*, +128 m_out_Entity:CVDynamic*, +130 f01b5f96:CV*, +138 5de7afe7:CV*, +140 d4619244:u8, +141 758a67e0:u8 | `0x15d9df0` 0x150 p2 | 3 / 3 |
| `20569e48` | *STUStatescriptStateForceAimDir* | SSState | 0x130 | +100 m_forWeapon:ref, +110 m_dir:CV*, +118 m_onEntity:CV*, +120 51dbf288:CV*, +128 c97f2482:u32 | → STUStatescriptState | 3 / 2 |
| `74cb0002` | STUStatescriptStatePossessControl | SSState | 0x118 | +100 m_possessor:CV*, +108 743b15a6:CV*, +110 54d710eb:u8 | → STUStatescriptState | 3 / 2 |
| `3a846155` | STUStatescriptStatePossessView | SSState | 0x110 | +100 m_possessor:CV*, +108 d7148c41:CV* | → STUStatescriptState | 3 / 2 |
| `22fe9543` | STUStatescriptStateRayCast | SSState | 0x150 | +100 m_ignoreEntities:CV*[], +110 m_startPosWS:CV*, +118 m_dirWS:CV*, +120 m_castLength:CV*, +128 m_castFilter:CVFilter*, +130 m_out_HitSomething:CVDynamic*, +138 m_out_HitEntity:CVDynamic*, +140 m_out_HitPointWS:CVDynamic*, +148 m_out_HitNormal:CVDynamic* | `0x15d3ec0` 0xf0 p2; `0x15d7540` 0xf0 p1 | 3 / 3 |
| `07167219` | *STUStatescriptStateSetLightingLayer* | SSState | 0x108 | +100 0c7b4e74:CV* | `0x15c72f8` 0x150 p2 | 3 / 3 |
| `06fcca5d` | STUStatescriptStateSuppressMovementPrediction | SSState | 0x100 |  | `0x15e60d8` 0xb8 p2 | 3 / 3 |
| `27984fc9` | *STUStatescriptStateTimeInArray* | SSState | 0x120 | +100 edfb51d3:CV*, +108 m_value:CV*, +110 m_out_Time:CVDynamic*, +118 m_defaultTime:CV* | `0x16060c0` 0xc0 p0 | 3 / 2 |
| `3f67b31b` |  | SSState | 0x110 | +100 6697832a:f26f968d*, +108 29ff2457:u8 | `0x15c77f0` 0x1b0 p2 | 3 / 3 |
| `52c50300` |  | SSState | 0x110 | +100 m_color:CV*, +108 m_amount:CV* | `0x15dd750` 0x150 p2 | 3 / 3 |
| `78146b78` |  | SSState | 0x100 |  | `0x15dc638` 0x150 p2 | 3 / 2 |
| `7aff7efe` |  | SSState | 0x118 | +100 7918d260:CV*, +108 0c650485:CV*, +110 5c6278b7:CV* | `0x15f00e0` 0x150 p2 | 3 / 3 |
| `97315c42` |  | SSState | 0x140 | +100 d400f59a:SSOutputPlug*, +108 91b4d548:SSOutputPlug*, +110 638541a5:SSOutputPlug*, +118 4333383d:CVDynamic*, +120 e38db4a1:CVDynamic*, +128 1eb7273b:CVDynamic*, +130 6da858cf:CVDynamic*, +138 767c4956:CVDynamic* | → STUStatescriptState | 3 / 3 |
| `a6fbbd49` |  | 09fb0449 | 0x128 | +108 f31b3961:CV*, +110 17167852:CV*, +118 m_nearScore:CV*, +120 m_farScore:CV* | → STUStatescriptState | 3 / 3 |
| `dda31b5c` |  | SSStateTargetFilterBase | 0x118 | +108 d9a06007:CV*, +110 838b1ba6:CV* | → STUStatescriptState | 3 / 3 |
| `e9ac5b41` |  | SSState | 0x100 |  | `0x15de4e8` 0x150 p2 | 3 / 3 |
| `f2163ed4` |  | SSStateTargetFilterBase | 0x110 | +108 162ec97a:CV* | → STUStatescriptState | 3 / 3 |
| `ed81c7fb` | STUStatescriptStateAimSpeed | SSState | 0x108 | +100 a8cf697b:CV* | `0x15d4118` 0xe0 p2 | 2 / 2 |
| `429991a7` | STUStatescriptStateBlinkEffect | SSState | 0x128 | +100 5094068c:8dcb62ea*, +108 m_startEffect:SSEffect*, +110 m_endEffect:677fd652*, +118 m_ribbonEffect:677fd652*, +120 25dd61e6:CV* | `0x15d4fc8` 0xf0 p2 | 2 / 2 |
| `985bcc76` | STUStatescriptStateDeflectProjectilesEffects | SSState | 0x120 | +100 ea946900:CV*, +108 ecdc6fd3:SSOutputPlug*, +110 m_out_Pos:CVDynamic*, +118 1dbd2323:CVDynamic* | `0x15d2970` 0x290 p2 | 2 / 2 |
| `26cb90c0` | STUStatescriptStateExtender | SSState | 0x1a8 | +100 m_out_Pos:CVDynamic*, +108 ebba30ab:CVDynamic*, +110 7edb811b:CVDynamic*, +118 5dbaf065:CVDynamic*, +120 d41af241:CVDynamic*, +128 ec477088:CVDynamic*, +130 76609896:CVDynamic*, +138 40da3d88:CVDynamic*, +140 cbcb0fb0:CVDynamic*, +148 e865d77a:CVDynamic*, +150 19ab05f7:CVFilter*, +158 1e4a0a7d:CV*, +160 m_sourceHardpoint:CV*, +168 m_speed:CV*, +170 9feaa98b:CV*, +178 m_weaponAim:CV*, +180 6523759e:CV*, +188 ab3b6f55:CV*, +190 19421178:CV*, +198 ff27a328:CV*, +1a0 27cae126:CV* | `0x15d5228` 0x120 p2; `0x15d6330` 0x120 p1 | 2 / 2 |
| `f533c587` | *STUStatescriptStateGlobalSound* | SSState | 0x108 | +100 48663e64:CV* | `0x15dc298` 0x160 p2 | 2 / 1 |
| `81f33242` | *STUStatescriptStateHideCosmeticEntity* | SSState | 0x108 | +100 933a1b70:CV* | `0x15de740` 0x160 p2 | 2 / 1 |
| `648f62a7` | *STUStatescriptStatePlacedCamera* | SSState | 0x118 | +100 7c8ce224:7426d7ef*, +108 3b363bca:2309c11b*, +110 d0136ec2:CV* | `0x15f11f0` 0x160 p2 | 2 / 2 |
| `9608418a` | *STUStatescriptStatePlayReel* | SSState | 0x120 | +100 53e84aef:CV*, +108 d32654db:CV*, +110 m_onAbortPlug:SSOutputPlug*, +118 m_onFinishedPlug:SSOutputPlug* | `0x15f2018` 0xc0 p2 | 2 / 1 |
| `af1de228` | *STUStatescriptStateReelReady* | SSState | 0x128 | +100 53e84aef:CV*, +108 d32654db:CV*, +110 37d3e214:CVDynamic*, +118 m_onAbortPlug:SSOutputPlug*, +120 m_onFinishedPlug:SSOutputPlug* | `0x15f1db0` 0xc0 p2 | 2 / 1 |
| `c2eeee16` | STUStatescriptStateSteer | f1c8d756 | 0x120 | +108 110b8981:CV*, +110 fc80592d:CV*, +118 d9e029c6:CV* | `0x1619a20` 0xb8 p1 | 2 / 1 |
| `bf733a32` | *STUStatescriptStateTimeScaleAnimation* | SSState | 0x108 | +100 m_timeScale:CV* | `0x1617f18` 0xe8 p1 | 2 / 2 |
| `86049103` | *STUStatescriptStateUXDialogBox* | SSStateUXBase | 0x170 | +150 b206aafe:SSOutputPlug*, +158 253fa200:SSOutputPlug*, +160 5d3f9849:SSOutputPlug*, +168 m_out_Text:CV* | `0x15cb590` 0x3f0 p2 | 2 / 2 |
| `e4e43d57` | *STUStatescriptStateUXRadialMenu* | SSStateUXBase | 0x170 | +150 2bcf40bb:f807744f[], +160 790e597d:CVDynamic*, +168 a07d0959:SSOutputPlug* | `0x15cbc20` 0x400 p2 | 2 / 1 |
| `984cab99` | *STUStatescriptStateUXScoreboardScreen* | SSStateUXBase | 0x168 | +150 707da06d:fileref, +160 326366da:CV* | `0x15cd640` 0x410 p2 | 2 / 1 |
| `40313ff3` | *STUStatescriptStateUXToastQueue* | SSState | 0x130 | +100 5cbe813f:24ab1f0b*, +108 52a20668:24ab1f0b*, +110 f2eb32db:CV*, +118 c5ccab8c:CV*, +120 ff356e92:CVDynamic*, +128 m_out_Duration:CVDynamic* | `0x15d31a0` 0x5a0 p2 | 2 / 2 |
| `fa0fec4f` | STUStatescriptStateUnkillable | SSState | 0x108 | +100 m_target:CV* | → STUStatescriptState | 2 / 1 |
| `1d6e98aa` |  | SSStateUXViewModel | 0x188 |  | `0x15ec260` 0x400 p2 | 2 / 1 |
| `4adcd381` |  | SSState | 0x118 | +100 53e84aef:CV*, +108 d32654db:CV*, +110 m_onFinishedPlug:SSOutputPlug* | `0x15f2728` 0xb8 p2 | 2 / 1 |
| `5e4f2a57` |  | SSState | 0x100 |  | → STUStatescriptState | 2 / 2 |
| `6005e1a4` |  | SSStateTargetFilterBase | 0x110 | +108 7228126b:CV* | → STUStatescriptState | 2 / 2 |
| `6c6eceed` |  | SSState | 0x128 | +100 3182e3b4:591ad712*[], +110 96004a88:CV*, +118 m_windowTime:CV*, +120 450e7e78:af42564f* | → STUStatescriptState | 2 / 2 |
| `a4f3be65` |  | SSState | 0x110 | +100 m_entries:STUPublishEntry[] | `0x15c97c0` 0xd0 p2 | 2 / 2 |
| `bae88c4e` |  | SSState | 0x130 | +100 ef7fd201:ref, +110 5cbe813f:24ab1f0b*, +118 1c6696a5:CV*, +120 4594c65e:CV*, +128 d9cbca19:CV* | `0x15d38b0` 0x470 p2 | 2 / 1 |
| `c4f4b4bb` |  | 09fb0449 | 0x118 | +108 m_forEntity:CV*, +110 4a093108:CV* | → STUStatescriptState | 2 / 2 |
| `c6366130` |  | SSState | 0x128 | +100 m_out_Velocity:CV*, +108 fa1237b2:CV*, +110 m_entity:CV*, +118 m_offset:CV*, +120 m_hardPoint:CV* | `0x161a3c0` 0x170 p1 | 2 / 1 |
| `cf08df77` |  | SSState | 0x110 | +100 5fbce770:CV*, +108 53e84aef:enum | `0x15f24d0` 0xc8 p2 | 2 / 2 |
| `de5f46ef` |  | SSStateUXViewModel | 0x188 |  | `0x15ed270` 0x400 p2 | 2 / 1 |
| `f0f7920b` |  | SSState | 0x108 | +100 cac58ff9:CV* | → STUStatescriptState | 2 / 2 |
| `f84fc1e2` |  | SSState | 0x120 | +100 6d047768:CV*, +108 m_onAbortPlug:SSOutputPlug*, +110 m_onFinishedPlug:SSOutputPlug*, +118 1bff3867:u8 | `0x15c8900` 0xc0 p2; `0x15c9c80` 0xb8 p0 | 2 / 1 |
| `fea1e74b` | STUStatescriptStateAccelerate | f1c8d756 | 0x118 | +108 c5c517e5:CV*, +110 m_terminalVelocity:CV* | `0x1619550` 0xb8 p1 | 1 / 1 |
| `37d27802` | *STUStatescriptStateAimLead* | SSStateAimRotate | 0x150 | +148 cc58884e:CV* | → STUStatescriptState | 1 / 1 |
| `10fc71ba` | STUStatescriptStateCameraChase | SSState | 0x108 | +100 d135915f:CV* | `0x15f1b58` 0xe0 p2 | 1 / 1 |
| `0119c536` | *STUStatescriptStateCapturePointEvents* | SSState | 0x170 | +100 46562068:SSOutputPlug*, +108 18e32949:SSOutputPlug*, +110 bdcd5456:SSOutputPlug*, +118 4c53a4e6:SSOutputPlug*, +120 186bc8f8:SSOutputPlug*, +128 03fc2892:24ab1f0b*, +130 01c2fd48:24ab1f0b*, +138 337c6af2:24ab1f0b*, +140 404fea29:24ab1f0b*, +148 be70c5df:CVDynamic*, +150 2410322f:CVDynamic*, +158 66c0c2dd:CVDynamic*, +160 87ab9dd1:CVDynamic*, +168 84e42246:CVDynamic* | `0x161b520` 0xb8 p1 | 1 / 1 |
| `76b28b97` | STUStatescriptStateContactSet | SSState | 0x118 | +100 m_contactSet:CV*, +108 dfcec171:CV*, +110 c2cebb43:CV* | `0x15dcb30` 0x160 p2 | 1 / 1 |
| `41bd3f6f` | STUStatescriptStateCriteria | SSState | 0x110 | +100 m_identifier:CV*, +108 m_targets:CV* | → STUStatescriptState | 1 / 1 |
| `5d6c48de` | *STUStatescriptStateDrawDebugLine* | SSState | 0x118 | +100 m_start:CV*, +108 m_end:CV*, +110 m_color:CV* | → STUStatescriptState | 1 / 1 |
| `bcaffa62` | *STUStatescriptStateDrawDebugSphere* | SSState | 0x118 | +100 0a63b9da:CV*, +108 m_radius:CV*, +110 m_color:CV* | `0x15c9098` 0xb8 p2 | 1 / 1 |
| `795e2668` | *STUStatescriptStateEffectEmitBulk* | SSStateUXScreen | 0x150 |  | `0x15e87a0` 0x410 p2 | 1 / 1 |
| `c5dcbfa1` | *STUStatescriptStateFilterArray* | SSState | 0x120 | +100 m_filter:CVFilter*, +108 edfb51d3:CV*, +110 m_out_Array:CV*, +118 a415be52:SSOutputPlug* | `0x1605e68` 0xb8 p0 | 1 / 1 |
| `029bf346` | STUStatescriptStateHistoryBuilder | SSState | 0x130 | +100 c282db8b:SSHistoryVar[], +110 efa4e44f:CV*, +118 3f20d7d2:CV*, +120 m_out_Handle:CVDynamic*, +128 cf5c0f0c:enum, +12c 7f702d66:enum | `0x15d7a00` 0xe0 p2; `0x15d7f48` 0xe0 p1 | 1 / 1 |
| `7211cf70` | STUStatescriptStateHistoryRewinder | SSState | 0x148 | +100 a9157cbb:CV*, +108 85f47ed7:CV*, +110 53222441:CV*, +118 05aaa790:CV*, +120 m_handle:CV*, +128 d970b2d6:CV*, +130 m_onAbortPlug:SSOutputPlug*, +138 m_onFinishedPlug:SSOutputPlug*, +140 c5a32914:enum | `0x15d7798` 0x5e0 p2 | 1 / 1 |
| `e452cc91` | *STUStatescriptStateLobbyWorld* | SSState | 0x110 | +100 m_map:CV*, +108 6e90dd7d:SSOutputPlug* | `0x15eb328` 0xc0 p2 | 1 / 1 |
| `13daa15b` | *STUStatescriptStateOverlayMap* | SSState | 0x170 | +100 m_focusPos:CV*, +108 da94571d:CV*, +110 3dcc3a9d:CV*, +118 b7e074b0:CV*, +120 8bed4be9:CV*, +128 dbfcad4e:CV*, +130 e7f19217:CV*, +138 368fa8d3:CV*, +140 0a82978a:CV*, +148 e57cb84c:CV*, +150 5b401d3d:CV*, +158 8f6c4811:CV*, +160 7e605b81:CV*, +168 52f2bd93:CV* | `0x15f0338` 0xb8 p2 | 1 / 1 |
| `e39c9d83` | STUStatescriptStateOverrideModelLook | SSState | 0x110 | +100 m_modelOwner:CV*, +108 m_lookOverride:CV* | `0x15ddd80` 0x170 p2 | 1 / 1 |
| `92f7915f` | *STUStatescriptStatePreserveButtonState* | SSState | 0x110 | +100 m_logicalButtons:enum[] | `0x15c8420` 0x150 p2 | 1 / 1 |
| `7bf31629` | *STUStatescriptStateScaleTime* | SSState | 0x120 | +100 4ad5330b:CV*, +108 b041c0e8:CV*, +110 53e591a4:CV*, +118 m_timeScalar:CV* | `0x1617b98` 0xc8 p1 | 1 / 1 |
| `d43bf558` | *STUStatescriptStateSetWind* | SSState | 0x108 | +100 bfa46edf:CV* | `0x15c7570` 0x160 p2 | 1 / 1 |
| `8d50485a` | STUStatescriptStateShockwave | SSState | 0x1a0 | +100 m_speed:CV*, +108 m_distance:CV*, +110 m_posEntity:CV*, +118 m_posOffset:CV*, +120 f6b861b5:CV*, +128 9d812041:CV*, +130 f8c29d41:CV*, +138 08f9cb86:CVFilter*, +140 m_filter:CVFilter*, +148 m_out_Targets:CVDynamic*, +150 170a891d:CV*, +158 37072941:CV*, +160 1b8c7dd8:CV*, +168 m_effect:CV*, +170 m_contactSet:CV*, +178 582d9f8f:CV*, +180 93e60229:CV*, +188 m_onAbortPlug:SSOutputPlug*, +190 m_onFinishedPlug:SSOutputPlug*, +198 5e4a73de:enum, +19c 854cd6b8:u8, +19d c7293391:u8 | `0x15d4ab8` 0x1e0 p2; `0x15d8dd0` 0xd8 p1 | 1 / 1 |
| `1787a82d` | STUStatescriptStateSprayEffect | SSState | 0x120 | +100 m_sprayUnlock:CV*, +108 86d36ec7:CV*, +110 71334683:SSOutputPlug*, +118 fadf5cbf:SSOutputPlug* | `0x15dbef8` 0x1a0 p2; `0x15dd168` 0x100 p1 | 1 / 1 |
| `76391dd3` | STUStatescriptStateTrackSlamming | SSState | 0x110 | +100 84cb6a46:CV*, +108 m_out_Slammed:CVDynamic* | `0x1619c88` 0xb8 p1 | 1 / 1 |
| `6dcadbf1` | *STUStatescriptStateUXAllHeroesScreen* | SSStateUXBase | 0x158 | +150 cebf46c8:CV* | `0x15ebf48` 0x400 p2 | 1 / 1 |
| `46b63a82` | *STUStatescriptStateUXChatControl* | SSState | 0x120 | +100 a10fb05b:CV*, +108 24aab0ef:CV*, +110 06fb9c19:CV*, +118 6eee4fbf:CV* | `0x15d08e0` 0xb8 p2 | 1 / 1 |
| `245a6b2a` | *STUStatescriptStateUXHeroCustomizeScreen* | SSStateUXBase | 0x170 | +150 m_hero:CV*, +158 fc52656e:CVDynamic*, +160 6abe7d7a:CVDynamic*, +168 e1f27c99:CVDynamic* | `0x15cc260` 0x410 p2 | 1 / 1 |
| `071c1efc` | STUStatescriptStateUXHeroHelp | SSStateUXBase | 0x158 | +150 m_hero:CV* | `0x15cbba0` 0x420 p2 | 1 / 1 |
| `8f23fa30` | *STUStatescriptStateUXHeroSelectScreen* | SSStateUXBase | 0x1c0 | +150 20090240:fileref, +160 879982dd:CVDynamic*, +168 39a8b181:CVDynamic*, +170 ca0d156d:CVDynamic*, +178 496641cd:CVDynamic*, +180 m_out_Offset:CVDynamic*, +188 m_out_Yaw:CVDynamic*, +190 m_out_Pitch:CVDynamic*, +198 m_out_Roll:CVDynamic*, +1a0 ca769358:CVDynamic*, +1a8 466ecc78:CV*, +1b0 163e7db5:SSOutputPlug*, +1b8 c59464ce:SSOutputPlug* | `0x15da910` 0x440 p2 | 1 / 1 |
| `a9d1db82` | *STUStatescriptStateUXHeroSheetScreen* | SSStateUXBase | 0x158 | +150 m_heroGUID:CV* | `0x15eb8c8` 0x3f0 p2 | 1 / 1 |
| `eac1d3ca` | *STUStatescriptStateUXKillCam* | SSStateUXBase | 0x150 |  | `0x15d1158` 0x3f0 p2 | 1 / 1 |
| `0e78be20` | *STUStatescriptStateUXLobby* | SSState | 0x110 | +100 edf666be:CVDynamic*, +108 97cccc8a:CVDynamic* | `0x15ee0d0` 0xb8 p2 | 1 / 1 |
| `5c6337eb` | *STUStatescriptStateUXMatchStats* | SSStateUXBase | 0x198 | +150 707da06d:fileref, +160 c0ef4fd7:CVDynamic*, +168 b990e415:CVDynamic*, +170 e68f51d6:SSOutputPlug*, +178 a076d623:SSOutputPlug*, +180 3e4401e8:CVDynamic*, +188 85227aeb:CV*, +190 1184747c:CV* | `0x15cd320` 0x440 p2 | 1 / 1 |
| `23a384f3` | STUStatescriptStateUXMatchmaking | SSStateUXBase | 0x150 |  | `0x15d0b48` 0x3f0 p2 | 1 / 1 |
| `7ab92812` | *STUStatescriptStateUXMenuGamepadSettings* | SSStateUXBase | 0x150 |  | `0x15ef338` 0x3e0 p2 | 1 / 1 |
| `ed25e9f2` | *STUStatescriptStateUXMenuGammaScreen* | SSStateUXBase | 0x168 | +150 21c9d8b7:SSOutputPlug*, +158 m_out_Error:CV*, +160 12aa58b2:CV* | `0x15ee6d8` 0x410 p2 | 1 / 1 |
| `0de81a01` | *STUStatescriptStateUXMenuGraphicSettings* | SSStateUXBase | 0x168 | +150 21c9d8b7:SSOutputPlug*, +158 m_out_Error:CV*, +160 12aa58b2:CV* | `0x15eea10` 0x570 p2 | 1 / 1 |
| `5c3a5c93` | *STUStatescriptStateUXMenuMouseSettings* | SSStateUXBase | 0x150 |  | `0x15ef038` 0x3e0 p2 | 1 / 1 |
| `d7f0e8e2` | *STUStatescriptStateUXMenuSoundSettings* | SSStateUXBase | 0x168 | +150 21c9d8b7:SSOutputPlug*, +158 m_out_Error:CV*, +160 12aa58b2:CV* | `0x15efca0` 0x400 p2 | 1 / 1 |
| `a66e33f7` | *STUStatescriptStateUXNextMap* | SSStateUXBase | 0x168 | +150 m_out_Map:CVDynamic*, +158 6b629dbe:CVDynamic*, +160 ea3391b6:CVDynamic* | `0x15e6f28` 0x3f0 p2 | 1 / 1 |
| `ded0a3a1` | *STUStatescriptStateUXPOTGCam* | SSStateUXBase | 0x150 |  | `0x15d04c0` 0x3f0 p2 | 1 / 1 |
| `7f336b68` | *STUStatescriptStateUXPartyScreen* | SSStateUXBase | 0x158 | +150 68ea86fe:CV* | `0x15ea9f0` 0x420 p2 | 1 / 1 |
| `56aef215` | *STUStatescriptStateUXProfileScreen* | SSStateUXBase | 0x158 | +150 d28a289b:CV* | `0x15e90c8` 0x420 p2 | 1 / 1 |
| `e0309618` | STUStatescriptStateUXProgression | SSStateUXBase | 0x168 | +150 338887e8:CV*, +158 133f139c:CV*, +160 f5ce9879:CV* | `0x15cbf18` 0x400 p2 | 1 / 1 |
| `4f95ce09` | *STUStatescriptStateUXVoiceChatScreen* | SSStateUXScreen | 0x150 |  | `0x15caf58` 0x3e0 p2 | 1 / 1 |
| `174842b0` | *STUStatescriptStateUXWinningTeam* | SSStateUXBase | 0x1d8 | +150 m_arrangement:SSHeroArrangement, +180 46e08f0a:CVDynamic*, +188 m_out_Skins:CVDynamic*, +190 m_out_Offsets:CVDynamic*, +198 m_out_Yaws:CVDynamic*, +1a0 c454fa42:CVDynamic*, +1a8 00d0a512:CVDynamic*, +1b0 1bbf3383:CVDynamic*, +1b8 m_out_Scales:CVDynamic*, +1c0 690266e3:CVDynamic*, +1c8 7e1fe7f9:CVDynamic*, +1d0 1f8c91a9:SSOutputPlug* | `0x15e76d8` 0x480 p2 | 1 / 1 |
| `12aa4f0f` | *STUStatescriptStateValidateWidth* | SSState | 0x168 | +100 m_position:CV*, +108 m_direction:CV*, +110 45561961:CV*, +118 92bdab20:CV*, +120 b73d0304:CV*, +128 f92d850c:CV*, +130 m_castFilter:CVFilter*, +138 5556c513:8dcb62ea*, +140 8cf01b33:SSOutputPlug*, +148 130ac01b:CVDynamic*, +150 b43e4062:CVDynamic*, +158 dccef747:CVDynamic*, +160 f5b00a5f:CVDynamic* | `0x15d5c10` 0xd0 p2; `0x15d6cc8` 0xc8 p1 | 1 / 1 |
| `1403b991` |  | SSStateUXViewModel | 0x198 | +188 9aebad88:CV*, +190 m_out_Password:CV* | `0x15ecbf0` 0x410 p2 | 1 / 1 |
| `1c214767` |  | SSStateUXBase | 0x160 | +150 m_hero:CV*, +158 81c71ffc:CVDynamic* | → STUStatescriptStateUXBase | 1 / 1 |
| `1e458e5c` |  | SSState | 0x108 | +100 0ab6f7cd:CV* | `0x15d1480` 0x150 p2 | 1 / 1 |
| `22226888` |  | SSStateUXBase | 0x150 |  | `0x15ef988` 0x450 p2 | 1 / 1 |
| `241069ef` |  | SSState | 0x130 | +100 m_targetEntity:CV*, +108 0db9c634:CV*, +110 2c75f177:CV*, +118 20623cab:CV*, +120 c99c08f2:CV*, +128 5d56bf0c:CV* | `0x15f0cc8` 0x160 p2 | 1 / 1 |
| `26b2c9f4` |  | SSState | 0x100 |  | `0x15f2270` 0xd0 p2 | 1 / 1 |
| `2785fbdd` |  | SSState | 0x138 | +100 m_color:CV*, +108 78970ef2:CV*, +110 70562e7c:CV*, +118 ef1c0e48:CV*, +120 2f50d884:CV*, +128 7a7c9915:CV*, +130 3fa1152e:CV* | `0x15cc7e0` 0xe0 p2 | 1 / 1 |
| `35851126` |  | SSStateUXBase | 0x158 | +150 m_heroGUID:CV* | `0x15eb5a0` 0x400 p2 | 1 / 1 |
| `3d58754f` |  | SSStateUXViewModel | 0x190 | +188 m_error:CV* | `0x15ead18` 0x410 p2 | 1 / 1 |
| `43bf9797` |  | SSStateUXViewModel | 0x188 |  | `0x15d1800` 0x400 p2 | 1 / 1 |
| `475b23c4` |  | SSStateUXViewModel | 0x190 | +188 e0bbec86:SSOutputPlug* | `0x15ed5a8` 0x420 p2 | 1 / 1 |
| `58d7d94f` |  | SSStateUXBase | 0x158 | +150 cebf46c8:CV* | `0x15ec5a0` 0x400 p2 | 1 / 1 |
| `5a007f21` |  | SSState | 0x100 |  | → STUStatescriptState | 1 / 1 |
| `5e494675` |  | SSStateUXScreen | 0x158 | +150 3bfeb018:CV* | `0x15e8a98` 0x400 p2 | 1 / 1 |
| `6716e4b9` |  | SSStateUXBase | 0x160 | +150 m_out_Name:CV*, +158 d49879f4:SSOutputPlug* | `0x15e9750` 0x410 p2 | 1 / 1 |
| `70e4c5c8` |  | SSStateUXViewModel | 0x190 | +188 4338fed5:CV* | `0x15ec8b8` 0x420 p2 | 1 / 1 |
| `73661e44` |  | SSStateUXBase | 0x150 |  | `0x15eb030` 0x3f0 p2 | 1 / 1 |
| `73ceedb4` |  | SSState | 0x100 |  | `0x15e7228` 0xd0 p2 | 1 / 1 |
| `7ffafe7b` |  | SSState | 0x128 | +100 5de09754:CV*, +108 e4e365d4:CV*, +110 m_distance:CV*, +118 91a72ec4:CV*, +120 2e3dc86d:CV* | `0x15f1448` 0xc0 p2 | 1 / 1 |
| `82505a1b` |  | SSState | 0x118 | +100 m_target:CV*, +108 2daaa3ef:CV*, +110 d79c6a6b:CV* | `0x1616f68` 0xb8 p1 | 1 / 1 |
| `893fa726` |  | SSState | 0x140 | +100 bf723e68:7426d7ef*, +108 8714b5bc:2309c11b*, +110 f9e75c9a:CV*, +118 0a9765eb:CV*, +120 0db9c634:CV*, +128 21ae0c2d:CV*, +130 4f1e34e2:CV*, +138 e3c9a2bd:CVDynamic* | `0x15f0908` 0x160 p2 | 1 / 1 |
| `9f67c194` |  | SSStateUXBase | 0x158 | +150 10e31d91:SSOutputPlug* | `0x15e8db0` 0x400 p2 | 1 / 1 |
| `ba8e336e` |  | SSState | 0x100 |  | `0x15d0250` 0xb8 p2 | 1 / 1 |
| `be66064d` |  | SSStateUXBase | 0x158 | +150 09aace5e:CV* | `0x15eed40` 0x3f0 p2 | 1 / 1 |
| `beff6ab0` |  | SSStateUXViewModel | 0x198 | +188 5c89f701:CV*, +190 52a64439:CV* | `0x15ecf40` 0x410 p2 | 1 / 1 |
| `c009e9d5` |  | SSState | 0x110 | +100 m_values:SSForwardValueToGlobalScriptPair[] | `0x15c70a0` 0xb8 p2 | 1 / 1 |
| `c4ba3118` |  | SSStateUXScreen | 0x168 | +150 7fe4a51e:CV*, +158 2b6a3b88:CV*, +160 207167f5:CV* | `0x15ea3b0` 0x3f0 p2 | 1 / 1 |
| `c8c71bbc` |  | f1c8d756 | 0x120 | +108 m_climbTime:CV*, +110 6e1a2648:CV*, +118 m_onFinishedPlug:SSOutputPlug* | `0x16197b8` 0xb8 p1 | 1 / 1 |
| `cdb394a5` |  | SSStateUXBase | 0x1b0 | +150 865b03fa:CVDynamic*, +158 m_out_Skin:CVDynamic*, +160 m_out_Offset:CVDynamic*, +168 m_out_Yaw:CVDynamic*, +170 m_out_Pitch:CVDynamic*, +178 m_out_Roll:CVDynamic*, +180 m_out_Anim:CVDynamic*, +188 420601eb:CVDynamic*, +190 c454fa42:CVDynamic*, +198 e96831c1:CVDynamic*, +1a0 2bb05d8e:CVDynamic*, +1a8 1f8c91a9:SSOutputPlug* | `0x15e7a30` 0x400 p2 | 1 / 1 |
| `f536a60f` |  | f1c8d756 | 0x120 | +108 dfa07e24:CV*, +110 bf6d8f58:CV*, +118 8e3d03b5:STUAnimCurve* | `0x161a618` 0xc0 p1 | 1 / 1 |
| `62a0012f` | *STUStatescriptStateAIBehavior* | SSState | 0x138 | +100 48382539:653419d9*[], +110 5c967329:CV*[], +120 48cebbb6:CV*, +128 261852a2:24ab1f0b*, +130 b0368640:SSOutputPlug* | → STUStatescriptState | – |
| `0e25f8e4` | *STUStatescriptStateAIFaceVector* | SSStateAIFacing | 0x120 | +110 m_baseEntity:CV*, +118 89f8aba4:CV* | `0x1622560` 0xc8 p1 | – |
| `ac2d7abe` | *STUStatescriptStateAIFacing* | SSState | 0x110 | +100 m_priority:CV*, +108 m_bodyEntity:CV* | → STUStatescriptState | – |
| `975e8048` | *STUStatescriptStateAIPathing* | SSState | 0x190 | +100 ee2b6e54:str, +110 b80b6a55:i32[], +120 aba93a83:CV*, +128 a76e0254:CV*, +130 bf857ff4:8dcb62ea*, +138 eb8982d7:SSOutputPlug*, +140 0a659592:SSOutputPlug*, +148 44fe49e1:SSOutputPlug*, +150 7865b864:SSOutputPlug*, +158 a71261e0:SSOutputPlug*, +160 f8604c06:SSOutputPlug*, +168 2301e989:24ab1f0b*, +170 0cae80a0:CV*, +178 dbed3062:CVDynamic*, +180 f27c4360:CVDynamic*, +188 0e5ea405:CVDynamic* | → STUStatescriptState | – |
| `dff7e7a8` | STUStatescriptStateAimBase | SSState | 0x138 | +100 m_forWeapon:ref, +110 be83b9d2:CV*, +118 285ba9da:CV*, +120 6f2d1586:CV*, +128 39f65a09:a674a29c*, +130 c97f2482:u32 | → STUStatescriptState | – |
| `1ffbc4df` | *STUStatescriptStateAutoFacing* | SSState | 0x120 | +100 m_targetEntity:CV*, +108 m_targetHardpoint:CV*, +110 d02a1fde:CV*, +118 fbe0b88d:u8, +119 cd15116e:u8 | `0x15e5e80` 0x150 p2 | – |
| `f16bff1b` | *STUStatescriptStateAutoInput* | SSState | 0x120 | +100 m_command:CV*, +108 m_onAbortPlug:SSOutputPlug*, +110 m_onFinishedPlug:SSOutputPlug*, +118 1c87c873:u8, +119 4ee89681:u8 | `0x15e5c28` 0x150 p2 | – |
| `01d127ec` | STUStatescriptStateCounter | SSState | 0x170 | +100 cc3dcc5a:CVDynamic*, +108 c46a0b04:CV*, +110 170e6d20:CV*, +118 52c3d7b4:CV*, +120 ad62bf18:CV*, +128 f1fd455c:CV*, +130 c268dd22:SSOutputPlug*, +138 6f779e5e:SSOutputPlug*, +140 ccb64f5b:SSOutputPlug*, +148 0be0a889:SSOutputPlug*, +150 e5f7b879:SSOutputPlug*, +158 0a721709:SSOutputPlug*, +160 m_onAbortPlug:SSOutputPlug*, +168 m_onFinishedPlug:SSOutputPlug* | `0x1605c10` 0xc0 p0 | – |
| `aa488c28` | *STUStatescriptStateImpactEffectOverride* | SSState | 0x108 | +100 m_effectGUID:CV* | `0x15d4390` 0x170 p2 | – |
| `e22c4db2` | *STUStatescriptStateLaunchVelocity* | SSState | 0x130 | +100 005269ee:CV*, +108 m_startPos:CV*, +110 m_endPos:CV*, +118 m_targetVelocity:CV*, +120 132c9273:5fa743d8*, +128 8db87969:CVDynamic* | `0x16192f8` 0xb8 p1 | – |
| `012a1554` | *STUStatescriptStatePathConstraint* | 4e86ba5b | 0x110 |  | → STUStatescriptState | – |
| `1cc48157` | *STUStatescriptStateProjectedMovement* | SSState | 0x158 | +100 m_entity:CV*, +108 89f6c2a7:CV*, +110 m_initialVelocity:CV*, +118 m_isCrouching:CV*, +120 e3ff2574:CV*, +128 86549440:CV*, +130 c6433bc1:CV*, +138 m_gravityScale:CV*, +140 bd02981e:CV*, +148 m_simulationTime:CV*, +150 b72a494a:CVDynamic* | `0x15e6330` 0xb8 p2 | – |
| `87a2edd1` | STUStatescriptStateSequencer | SSState | 0x130 | +100 eb2a1736:SSOutputPlug*[], +110 3ff027cf:CV*, +118 69b11538:CV*, +120 m_onAbortPlug:SSOutputPlug*, +128 m_onFinishedPlug:SSOutputPlug* | `0x1605760` 0xd8 p0 | – |
| `06bcf0df` | STUStatescriptStateSubScriptBase | SSState | 0x128 | +100 7eb65d0d:SSSubScriptInitData[], +110 m_onAbortPlug:SSOutputPlug*, +118 m_onFinishedPlug:SSOutputPlug*, +120 db3a2efb:u8, +121 916d18ce:u8 | → STUStatescriptState | – |
| `985d49fe` | *STUStatescriptStateTargetFilterBase* | SSState | 0x108 | +100 c97f2482:u32 | → STUStatescriptState | – |
| `59e5e3cc` | STUStatescriptStateTrackArea | SSState | 0x120 | +100 e23d910b:CV*, +108 m_entityRef:CVDynamic*, +110 m_filter:CVFilter*, +118 m_out_Targets:CVDynamic* | `0x1616d00` 0xd8 p1 | – |
| `091d24f8` | STUStatescriptStateTrackInput | SSState | 0x110 | +100 9a914b01:CVDynamic*, +108 ed967b97:CVDynamic* | `0x15cc578` 0x150 p2 | – |
| `8ba08edd` | STUStatescriptStateUXBase | SSState | 0x150 | +100 6fa954ac:1e89514b*[], +110 731e0121:af12fa9c*[], +120 m_screen:CV*, +128 5e04c6a1:CVFilter*, +130 43a0ed71:CV*, +138 m_out_Handle:CVDynamic*, +140 4c58eab7:enum, +144 79b11db3:u8, +145 4660832b:u8, +146 m_layer:u8, +147 3fe6ca23:u8, +148 c20122eb:u8, +149 5eacf59b:u8, +14a c684fa91:u8, +14b 5d089eb6:u8 | `0x15ceb18` 0xb8 p1 | – |
| `6c2502e0` | *STUStatescriptStateUpdatePayload* | SSState | 0x128 | +100 a8f9dafa:CVDynamic*, +108 995cb516:CVDynamic*, +110 a0e922d6:CV*, +118 bab53ff9:CV*, +120 ce6de719:CV* | → STUStatescriptState | – |
| `6e3b8ce5` | *STUStatescriptStateVoiceStimulus* | SSState | 0x130 | +100 m_stimulus:CV*, +108 m_targets:CV*, +110 3c7dbbca:CV*, +118 e0192e0b:CV*, +120 e341b9c4:CV*, +128 m_onFinishedPlug:SSOutputPlug* | `0x15c81c8` 0xb8 p2 | – |
| `bc3a55c5` | STUStatescriptStateWeaponAccuracy | SSState | 0x160 | +100 f7cdf1fb:CV*, +108 202643ba:CV*, +110 43bb1bdf:CV*, +118 9450a99e:CV*, +120 5c5f54bf:CV*, +128 8bb4e6fe:CV*, +130 add1d8ed:CV*, +138 7a3a6aac:CV*, +140 9706fddb:CV*, +148 40ed4f9a:CV*, +150 ff84a558:CVDynamic*, +158 286f1719:CVDynamic* | `0x1617460` 0xf0 p1 | – |
| `85d43f96` | STUStatescriptStateWeaponAim | SSState | 0x120 | +100 8384557d:CV*, +108 8ae409d0:CV*, +110 6ef8f0e2:CV*, +118 be21ec57:CV* | `0x15d21c8` 0x100 p2 | – |
| `01dcd515` |  | SSStatePathConstraint | 0x140 | +110 ace386a2:CV*, +118 3c909648:CV*, +120 m_radius:CV*, +128 m_targetPos:CV*, +130 m_height:CV*, +138 b0da11c5:CV* | → STUStatescriptState | – |
| `06593992` |  | SSState | 0x120 | +100 e59a5847:CV*, +108 b29b19af:CVDynamic*, +110 21d206b2:SSOutputPlug*, +118 m_type:enum | → STUStatescriptState | – |
| `09fb0449` |  | SSState | 0x108 | +100 c97f2482:u32 | → STUStatescriptState | – |
| `0f9dbb6a` |  | SSState | 0x128 | +100 m_targetEntity:CV*, +108 d4116221:CV*, +110 c97f2482:CV*, +118 5f07d680:CV*, +120 7afac20c:CV* | → STUStatescriptState | – |
| `112615fd` |  | 79010fa3 | 0x130 | +100 m_maxSpeed:CV*, +108 160c411b:CV*, +110 1ff735d1:CV*, +118 c5966dde:CV*, +120 91212cb6:CV*, +128 5fd022ae:CV* | → STUStatescriptState | – |
| `127d9492` |  | SSStateAIFacing | 0x128 | +110 4b82be57:CV*, +118 m_weaponID:CV*, +120 c4dd2493:CV* | `0x16220a0` 0xc8 p1 | – |
| `16e4c219` |  | SSStateAIFacing | 0x110 |  | `0x16227c0` 0xc8 p1 | – |
| `1efc56d9` |  | SSStateUXBase | 0x160 | +150 a9b82228:SSOutputPlug*, +158 2c1bd6a8:SSOutputPlug* | `0x15e9a88` 0x3e0 p2 | – |
| `2061837d` |  | f778ea2f | 0x120 | +118 a54cc132:CV* | → STUStatescriptState | – |
| `2a3b7c97` |  | SSStatePathConstraint | 0x118 | +110 m_targetPos:CV* | → STUStatescriptState | – |
| `305caa8f` |  | SSStateUXBase | 0x168 | +150 18ce38cf:SSOutputPlug*, +158 8c77e222:SSOutputPlug*, +160 m_out_Error:CV* | `0x15ea098` 0x3e0 p2 | – |
| `33d9a241` |  | 09fb0449 | 0x110 | +108 3c25570a:CV* | → STUStatescriptState | – |
| `347a7665` |  | SSStateUXBase | 0x160 | +150 b206aafe:SSOutputPlug*, +158 m_error:CV* | `0x15cdc98` 0x3f0 p2 | – |
| `36fbd221` |  | SSState | 0x148 | +100 m_shapeScale:CV*, +108 m_sourceEntity:CV*, +110 8ab9afaf:CV*, +118 8f5bb800:CV*, +120 1eb4ba3e:CV*, +128 2b89ed65:CVDynamic*, +130 e553ecc0:CVDynamic*, +138 7a26040d:SSOutputPlug*, +140 91c740bc:enum | `0x1617940` 0x5f0 p1 | – |
| `3f2220fe` |  | SSStateUXBase | 0x168 | +150 b206aafe:SSOutputPlug*, +158 m_error:CV*, +160 093bf2b2:CV* | → STUStatescriptStateUXBase | – |
| `4095db1d` |  | b553da1c | 0x160 | +120 m_ignoreEntities:CV*[], +130 8d01fe25:CV*, +138 2c75f177:CV*, +140 809462a2:CV*, +148 16747e40:CV*, +150 m_castRadius:CV*, +158 06936688:CV* | → STUStatescriptState | – |
| `435a5348` |  | SSState | 0x128 | +100 m_weapon:ref, +110 86b69a3e:CV*, +118 m_onEntity:CV*, +120 c97f2482:CV* | → STUStatescriptState | – |
| `4e86ba5b` |  | SSState | 0x110 | +100 b1b051cb:i32[] | → STUStatescriptState | – |
| `57f3b2c4` |  | SSState | 0x108 | +100 af8e8704:CV* | → STUStatescriptState | – |
| `616f509f` |  | SSState | 0x148 | +100 m_speed:CV*, +108 m_destinationRadius:CV*, +110 6cbdabbb:CV*, +118 ac9d3ea1:CV*, +120 7a2added:CV*, +128 7fe17913:CVDynamic*, +130 m_onAbortPlug:SSOutputPlug*, +138 m_onFinishedPlug:SSOutputPlug*, +140 1a985a27:SSOutputPlug* | `0x1618bc0` 0xb8 p1 | – |
| `79010fa3` |  | SSState | 0x100 |  | → STUStatescriptState | – |
| `7fbb9d3d` |  | 09fb0449 | 0x110 | +108 m_maxScore:CV* | → STUStatescriptState | – |
| `81565b97` |  | SSStateUXBase | 0x188 | +150 011415d9:SSOutputPlug*, +158 25a68fe0:SSOutputPlug*, +160 667f4fc1:SSOutputPlug*, +168 80b1a5f6:SSOutputPlug*, +170 05ee621a:SSOutputPlug*, +178 m_out_Error:CV*, +180 12aa58b2:CV* | `0x15ea6c8` 0x3f0 p2 | – |
| `823ad1be` |  | b553da1c | 0x140 | +120 eefeec53:CV*, +128 f93fd47a:CV*, +130 92e298ef:CV*, +138 879d7882:CV* | → STUStatescriptState | – |
| `892645a3` |  | SSStatePathConstraint | 0x110 |  | → STUStatescriptState | – |
| `973df238` |  | SSState | 0x150 | +100 m_shapeScale:CV*, +108 m_sourceEntity:CV*, +110 m_targetEntity:CV*, +118 m_targetHardpoint:CV*, +120 1b4af499:CV*, +128 eea00369:CV*, +130 a019e487:CVDynamic*, +138 58afef2f:CVDynamic*, +140 9675ee8a:CVDynamic*, +148 91c740bc:enum | `0x16176d8` 0x660 p1 | – |
| `9acca46c` |  | SSStateUXBase | 0x158 | +150 13849919:SSOutputPlug* | `0x15e9438` 0x3e0 p2 | – |
| `9da1fce6` |  | SSStateUXBase | 0x170 | +150 bf29b152:SSOutputPlug*, +158 e6152a56:SSOutputPlug*, +160 8c77e222:SSOutputPlug*, +168 m_out_Error:CV* | `0x15e9da0` 0x3f0 p2 | – |
| `9f5c2321` |  | SSStateUXBase | 0x160 | +150 b206aafe:SSOutputPlug*, +158 093bf2b2:CV* | `0x15cb888` 0x3f0 p2 | – |
| `a522e13a` |  | SSState | 0x110 | +100 m_escortEntity:CV*, +108 m_requesterEntity:CV* | → STUStatescriptState | – |
| `a8e96832` |  | b553da1c | 0x138 | +120 a1dc7286:CV*, +128 c9231ea1:CV*, +130 65b57be5:CV* | → STUStatescriptState | – |
| `aa1a1cd7` |  | b553da1c | 0x140 | +120 f2a974c0:CV*, +128 a1dc7286:CV*, +130 c9231ea1:CV*, +138 65b57be5:CV* | → STUStatescriptState | – |
| `b331c9d7` |  | b553da1c | 0x128 | +120 5819c2c1:CV* | → STUStatescriptState | – |
| `b553da1c` |  | f778ea2f | 0x120 | +118 1856bab7:CV* | → STUStatescriptState | – |
| `bed0b494` |  | SSStatePathConstraint | 0x120 | +110 5f5a826c:CV*, +118 2b68b937:CV* | → STUStatescriptState | – |
| `bee9571d` |  | b553da1c | 0x168 | +120 57ba0190:CV*, +128 e3ff9bc4:CV*, +130 dff2a49d:CV*, +138 5819c2c1:CV*, +140 fc08b907:CV*, +148 879d7882:CV*, +150 bffa64f6:CV*, +158 028fabc5:97bc16da*, +160 265ff2a5:enum | → STUStatescriptState | – |
| `c3410547` |  | 79010fa3 | 0x108 | +100 05cba5e3:CV* | → STUStatescriptState | – |
| `d3f788be` |  | SSState | 0x118 | +100 7095fec3:CV*, +108 m_onEntity:CV*, +110 a330e2db:CV* | → STUStatescriptState | – |
| `e217de17` |  | SSStateUXBase | 0x170 | +150 8051a462:SSOutputPlug*, +158 e6152a56:SSOutputPlug*, +160 8c77e222:SSOutputPlug*, +168 m_out_Error:CV* | `0x15cd9a0` 0x3f0 p2 | – |
| `e2557fe7` |  | SSState | 0x118 | +100 f8b6c20e:CV*, +108 m_ownerEntity:CV*, +110 91c740bc:7d34e861* | → STUStatescriptState | – |
| `e8b60f5a` |  | b553da1c | 0x148 | +120 4784161f:CV*, +128 623e960b:CV*, +130 f8b6c20e:CV*, +138 8568b5a1:CV*, +140 6ba16ff2:0c755d64* | → STUStatescriptState | – |
| `ec5e5b8e` |  | b553da1c | 0x148 | +120 511fec01:CV*, +128 92e298ef:CV*, +130 c51825ea:CV*, +138 c6ebc5a5:CV*, +140 5819c2c1:CV* | → STUStatescriptState | – |
| `ed329693` |  | f1c8d756 | 0x118 | +108 051ce8cf:CV*, +110 cdd23137:CV* | `0x1619ee0` 0xb8 p1 | – |
| `f1c8d756` |  | SSState | 0x108 | +100 m_priority:CV* | → STUStatescriptState | – |
| `f778ea2f` |  | 4e86ba5b | 0x118 | +110 b0a30177:CV* | → STUStatescriptState | – |

## Config vars (328)

| hash | name | parent | STU | own fields | runtime | uses |
|---|---|---|---|---|---|---|
| `f8cc195d` | STUConfigVarDynamic | CV | 0x20 | +10 4f6799b5:ref[] | `0x16067a8` 0x8 p0 | 11708 / 278 |
| `3ae7427e` | STUConfigVarInt | CVIntBase | 0x18 | +10 m_value:i32 | `0x16073e8` 0x8 p0 | 4291 / 255 |
| `ec502a81` | *STUConfigVarExpressionTyped* | CVExpression | 0x68 |  | `0x160af18` 0x8 p0 | 3226 / 254 |
| `261676f0` | STUConfigVarGetOwner | CVEntityID | 0x10 |  | `0x1621358` 0x8 p1 | 3042 / 238 |
| `5e745f2b` | STUConfigVarBool | CVBoolBase | 0x18 | +10 m_value:u8 | `0x1607068` 0x8 p0 | 2813 / 243 |
| `7b13a0d4` | STUConfigVarFloat | CVFloatBase | 0x18 | +10 m_value:f32 | `0x1606b28` 0x8 p0 | 2011 / 195 |
| `6324dd9f` | STUConfigVarIdentifier | CVResource | 0x20 | +10 m_identifier:fileref | `0x1609e78` 0x8 p0 | 829 / 177 |
| `ff7876f9` | STUConfigVarGameMessage | CVResource | 0x20 | +10 m_gameMessage:ref | `0x1609858` 0x8 p0 | 725 / 206 |
| `b709560a` | STUConfigVarEffect | CVResource | 0x20 | +10 m_effect:fileref | `0x160a1f8` 0x8 p0 | 681 / 172 |
| `8fe4a55c` | STUConfigVarHardPoint | CVResource | 0x20 | +10 m_hardPoint:ref | `0x160ac78` 0x8 p0 | 567 / 108 |
| `7ead98e5` | STUConfigVarFilter | CV | 0x68 | +10 d33fcc8f:u64, +18 1f8d859e:u64, +20 dbcfe34a:u64, +28 11661069:u64, +30 0b03a6d2:u64, +38 3f374297:u64, … | `0x1621898` 0x8 p1 | 484 / 134 |
| `2adc2e14` | *STUConfigVarStackPriority* | CVResource | 0x20 | +10 m_priority:ref | `0x160ad58` 0x8 p0 | 442 / 138 |
| `5c7964ff` | STUConfigVarLogicalButton | CVEnum | 0x18 | +10 m_logicalButton:enum | `0x1606968` 0x8 p0 | 421 / 117 |
| `824213f0` | STUConfigVarDataFlow | CVResource | 0x20 | +10 m_dataFlow:ref | `0x160a8f8` 0x8 p0 | 283 / 87 |
| `ee7800e7` | STUConfigVarAnimAlias | CVAnimationBase | 0x20 | +10 m_animAlias:fileref | `0x160a818` 0x8 p0 | 267 / 108 |
| `e53c2921` | *STUConfigVarEntityDefinition* | CVEntityDefinitionBase | 0x20 | +10 m_entityDef:ref | `0x160a9d8` 0x8 p0 | 253 / 90 |
| `cc3a08f4` | STUConfigVarExpression | CV | 0x68 | +10 m_expression:CVExpressionData, +58 m_configVars:CV*[] | `0x160ae38` 0x8 p0 | 253 / 81 |
| `d3d12aec` | *STUConfigVarUXIDLink* | CVResource | 0x20 | +10 m_IDLink:ref | `0x160a3b8` 0x8 p0 | 250 / 116 |
| `dcdf6679` | *STUConfigVarGetLocalPlayerPossessedView* | CVEntityID | 0x10 |  | `0x15e4cf0` 0x8 p2 | 224 / 65 |
| `d1d51f19` | STUConfigVarGetTeam | CVNumeric | 0x18 | +10 m_target:CV* | `0x161d8b8` 0x8 p1 | 198 / 56 |
| `a0dd8e27` |  | CVResource | 0x20 | +10 6784a862:ref | `0x160a498` 0x8 p0 | 198 / 112 |
| `3439f27f` | STUConfigVarUXDisplayText | CVResource | 0x20 | +10 m_displayText:fileref | `0x160a578` 0x8 p0 | 185 / 37 |
| `c6f01de9` | *STUConfigVarGetEntityPassesFilter* | CVBoolBase | 0x20 | +10 m_entity:CV*, +18 m_filter:CVFilter* | `0x16217b8` 0x8 p1 | 173 / 70 |
| `e9fe238d` | STUConfigVarUXScreen | CVResource | 0x20 | +10 m_screen:fileref | `0x160a2d8` 0x8 p0 | 162 / 90 |
| `b0bbdf5a` | STUConfigVarGetEntityPosition | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1620c58` 0x8 p1 | 153 / 48 |
| `7b44c3c9` | STUConfigVarTargetTag | CVResource | 0x20 | +10 2e97da6d:ref | `0x1609938` 0x8 p0 | 152 / 46 |
| `9fd77551` | STUConfigVarString | CVStringBase | 0x20 | +10 m_value:str | `0x1606f88` 0x8 p0 | 148 / 89 |
| `e778cb36` | *STUConfigVarHealthType* | CVIntBase | 0x18 | +10 36967fde:enum | `0x161d998` 0x8 p1 | 142 / 22 |
| `04e83493` | *STUConfigVarStatescriptGraph* | CVResource | 0x20 | +10 m_graph:ref | `0x160a658` 0x8 p0 | 134 / 57 |
| `31011e9b` | STUConfigVarTexture | CVResource | 0x20 | +10 m_texture:fileref | `0x160a038` 0x8 p0 | 133 / 96 |
| `dbf9195c` | STUConfigVarVec3 | CVVecBase | 0x20 | +10 m_value:81ebb9b0 | `0x1606dc8` 0x8 p0 | 127 / 48 |
| `cc28a9d6` | STUConfigVarAnimCategory | CVAnimationBase | 0x20 | +10 m_category:fileref | `0x1609cb8` 0x8 p0 | 126 / 49 |
| `8b31201d` | *STUConfigVarGetPrimaryPossessedEntity* | CVEntityID | 0x18 | +10 m_entity:CV* | `0x161f3d8` 0x8 p1 | 125 / 21 |
| `ee046439` | *STUConfigVarFormatString* | CVStringBase | 0x28 | +10 m_strings:CV*[], +20 0f5f7a8a:CV* | `0x15e2900` 0x8 p2 | 117 / 28 |
| `08f6ed3d` |  | CVResource | 0x18 | +10 7095fec3:CV* | `0x15e2200` 0x8 p2 | 110 / 76 |
| `7b51e72b` | STUConfigVarGetEntityName | CVStringBase | 0x20 | +10 m_entity:CV*, +18 c0b31b69:CV* | `0x16207f8` 0x8 p1 | 98 / 24 |
| `f062ba36` | STUConfigVarGetPetMaster | CVEntityID | 0x18 | +10 m_petEntity:CV* | `0x161ecd8` 0x8 p1 | 87 / 22 |
| `d4e0b6a8` | STUConfigVarColor | CVColorBase | 0x20 | +10 m_value:a9e19537 | `0x16066c8` 0x8 p0 | 86 / 32 |
| `4c31363c` | *STUConfigVarArrayContains* | CVBoolBase | 0x28 | +10 edfb51d3:CV*, +18 m_key:CV*, +20 m_value:CV* | `0x1607228` 0x8 p0 | 85 / 52 |
| `91c4af3f` | STUConfigVarGetEntityFacingVector | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1620ef8` 0x8 p1 | 85 / 40 |
| `3df41d8f` | *STUConfigVarRaycastType* | CVResource | 0x20 | +10 m_raycastType:ref | `0x1609318` 0x8 p0 | 82 / 36 |
| `3c9fb6d7` | STUConfigVarGetEntityHeadPos | CVEntityID | 0x18 | +10 m_entity:CV* | `0x1620478` 0x8 p1 | 76 / 39 |
| `0badb906` | STUConfigVarGetEntityYaw | CVNumeric | 0x18 | +10 m_entity:CV* | `0x1620638` 0x8 p1 | 71 / 42 |
| `3eb3d6ce` | STUConfigVarBone | CVResource | 0x20 | +10 m_bone:ref | `0x160ab98` 0x8 p0 | 70 / 28 |
| `5dd4ed21` | *STUConfigVarGetLocalPlayer* | CVEntityID | 0x10 |  | `0x15e5310` 0x8 p2 | 69 / 25 |
| `d5384f53` | *STUConfigVarGetPvPEntity* | CVEntityID | 0x10 |  | `0x161d458` 0x8 p1 | 66 / 28 |
| `56ad73f3` | *STUConfigVarGetRichTextString* | CVStringBase | 0x18 | +10 m_foregroundColor:CV* | `0x15e23c0` 0x8 p2 | 60 / 20 |
| `827387ee` | *STUConfigVarGetEntityVisualHardPointPosition* | CVVecBase | 0x20 | +10 m_entity:CV*, +18 m_hardPoint:CV* | `0x161f758` 0x8 p1 | 58 / 21 |
| `93fd530c` |  | CVResource | 0x10 |  | `0x15dfef0` 0x8 p2 | 58 / 1 |
| `fafb9471` |  | CVBoolBase | 0x28 | +10 1506e063:ref, +20 m_hero:CV* | `0x161cb98` 0x8 p1 | 56 / 1 |
| `1eff9521` | *STUConfigVarGetPrimaryPossessorEntity* | CVEntityID | 0x18 | +10 m_entity:CV* | `0x161c9d8` 0x8 p1 | 53 / 10 |
| `cf15cfa6` | STUConfigVarTeam | CVIntBase | 0x18 | +10 f26a2fc7:enum | `0x161db58` 0x8 p1 | 50 / 13 |
| `36c0ad84` |  | CVNumeric | 0x20 | +10 m_entity:CV*, +18 36967fde:CV* | `0x16202b8` 0x8 p1 | 50 / 10 |
| `3d225ef4` |  | CVNumeric | 0x20 | +10 m_entity:CV*, +18 36967fde:CV* | `0x1620398` 0x8 p1 | 50 / 10 |
| `1b6632cd` | STUConfigVarGetEntityExists | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x16215f8` 0x8 p1 | 44 / 26 |
| `c81f8768` |  | CVBoolBase | 0x20 | +10 a67f0501:CVDynamic*, +18 m_stackPriority:CV* | `0x1606a48` 0x8 p0 | 42 / 33 |
| `dcadc6c2` | STUConfigVarAura | CVResource | 0x20 | +10 m_aura:fileref | `0x1609d98` 0x8 p0 | 41 / 30 |
| `56a0d806` | STUConfigVarGetEntityForwardVector | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1620fd8` 0x8 p1 | 41 / 18 |
| `d466d90f` | STUConfigVarGetEntityController | CVEntityID | 0x18 | +10 m_controlledEntity:CV* | `0x161ebf8` 0x8 p1 | 38 / 24 |
| `9ec82686` | *STUConfigVarGetEntityVisualPosition* | CVVecBase | 0x18 | +10 m_entity:CV* | `0x161f678` 0x8 p1 | 37 / 20 |
| `083d93f0` |  | CVVecBase | 0x20 | +10 566e9b3e:CV*, +18 2c70ef65:CV* | `0x161d538` 0x8 p1 | 37 / 13 |
| `62659035` | STUConfigVarGetEntityPitch | CVNumeric | 0x18 | +10 m_entity:CV* | `0x1620718` 0x8 p1 | 36 / 27 |
| `13ba10f6` | STUConfigVarArray | CV | 0x20 | +10 m_configVars:CV*[] | `0x1606888` 0x8 p0 | 33 / 14 |
| `f5bb64ba` | *STUConfigVarFloatRandom* | CVFloatBase | 0x18 | +10 m_min:f32, +14 m_max:f32 | `0x1606c08` 0x8 p0 | 31 / 6 |
| `8e85bff5` | *STUConfigVarGetLocalPlayerPrimaryPossessable* | CVEntityID | 0x10 |  | `0x15e4eb0` 0x8 p2 | 31 / 14 |
| `722ea2e4` |  | CVBoolBase | 0x10 |  | `0x161c118` 0x8 p1 | 30 / 17 |
| `6723776e` | STUConfigVarGetEntityHealth | CVNumeric | 0x20 | +10 m_entity:CV*, +18 36967fde:CV* | `0x161ff38` 0x8 p1 | 28 / 12 |
| `8761967f` | *STUConfigVarUXAuthorityGrey* | CVIterable | 0x18 | +10 m_observedEntity:CV* | → STUConfigVar | 27 / 23 |
| `adfc9542` |  | CVBoolBase | 0x28 | +10 fb524c40:CV*, +18 1e7586b7:CV*, +20 df848896:CV* | `0x15e47b0` 0x8 p2 | 26 / 12 |
| `4f205faf` | *STUConfigVarGetGlobalEntity* | CVEntityID | 0x10 |  | `0x15e2740` 0x8 p2 | 25 / 18 |
| `80227402` |  | CVBoolBase | 0x20 | +10 3fbb7651:ref[] | `0x15e0de0` 0x8 p2 | 24 / 2 |
| `6bdad1c6` | *STUConfigVarGetEntityHeroName* | CVStringBase | 0x20 | +10 m_entity:CV*, +18 c0b31b69:CV* | `0x15dfc50` 0x8 p2 | 23 / 2 |
| `8193e95a` | *STUConfigVarGetCosmeticEntity* | CVEntityID | 0x20 | +10 m_entity:CV*, +18 933a1b70:CV* | `0x15dfe10` 0x8 p2 | 21 / 4 |
| `ce9fd8e1` | *STUConfigVarGetUsingGamepad* | CVBoolBase | 0x10 |  | `0x161bcb8` 0x8 p1 | 21 / 9 |
| `b0e8443b` | STUConfigVarGetAITarget | CVEntityID | 0x20 | +10 fbdafef2:CV*, +18 c97f2482:CV* | `0x161e5d8` 0x8 p1 | 20 / 5 |
| `70429452` | STUConfigVarGetEntityHardPointPosition | CVVecBase | 0x20 | +10 m_entity:CV*, +18 m_hardPoint:CV* | `0x1620e18` 0x8 p1 | 19 / 14 |
| `c29cf678` |  | CVBoolBase | 0x10 |  | `0x15e2ba0` 0x8 p2 | 19 / 15 |
| `73fe9b53` | STUConfigVarContactSet | CVResource | 0x20 | +10 m_contactSet:ref | `0x1609f58` 0x8 p0 | 18 / 11 |
| `79771a3a` | STUConfigVarGamePadVibration | CVResource | 0x20 | +10 m_vibration:ref | `0x16094d8` 0x8 p0 | 18 / 7 |
| `0e00b16e` |  | CVBoolBase | 0x10 |  | `0x15e2f20` 0x8 p2 | 18 / 6 |
| `7b95cc99` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15e3ef0` 0x8 p2 | 18 / 7 |
| `86b5ecea` |  | CVColorBase | 0x20 | +10 32410b8f:CV*, +18 dc1e3375:enum | `0x15df1d0` 0x8 p2 | 18 / 2 |
| `8f3d1c9d` |  | CVIterable | 0x10 |  | → STUConfigVar | 18 / 3 |
| `decb4aff` | *STUConfigVarFindEntity* | CVEntityID | 0x30 | +10 bc189043:CV*, +18 a8dcabcd:CV*, +20 84dd0e91:CV*, +28 m_filter:CVFilter* | `0x161d298` 0x8 p1 | 16 / 9 |
| `023e430a` | STUConfigVarGetEntityLeftVector | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1621278` 0x8 p1 | 16 / 11 |
| `9161fb46` |  | CVBoolBase | 0x10 |  | `0x15e31c0` 0x8 p2 | 16 / 10 |
| `062d774c` | *STUConfigVarGetWorldSpacePosition* | CVVecBase | 0x20 | +10 m_entity:CV*, +18 m_offset:CV* | `0x161baf8` 0x8 p1 | 15 / 4 |
| `13d7f101` | *STUConfigVarGetHeroLoadoutIcon* | CVGetHeroLoadoutBase | 0x20 |  | `0x15e0600` 0x8 p2 | 14 / 13 |
| `b5e9c66e` | STUConfigVarIsHeroSelectActive | CVBoolBase | 0x10 |  | `0x15e2580` 0x8 p2 | 14 / 14 |
| `b164e7c6` | *STUConfigVarGetEntityMaxHealth* | CVNumeric | 0x20 | +10 m_entity:CV*, +18 36967fde:CV* | `0x1620018` 0x8 p1 | 13 / 4 |
| `9f97c6bb` | *STUConfigVarGetEntityVisualFacingVector* | CVVecBase | 0x18 | +10 m_entity:CV* | `0x161f598` 0x8 p1 | 13 / 9 |
| `9f16f09a` | *STUConfigVarGetProjectileOwner* | CVEntityID | 0x20 | +10 480bcc63:CV*, +18 30aff321:enum | `0x1621438` 0x8 p1 | 13 / 5 |
| `acfe29ca` | STUConfigVarVec2 | CVVecBase | 0x18 | +10 m_value:vec2 | `0x1606ce8` 0x8 p0 | 13 / 4 |
| `51870208` |  | CVNumeric | 0x28 | +10 m_pos:CV*, +18 a3fe58ce:CV*, +20 m_sceneType:enum | `0x15e3630` 0x8 p2 | 13 / 8 |
| `e5e40e59` | STUConfigVarGetAIFacingVector | CVVecBase | 0x18 | +10 fbdafef2:CV* | `0x161e878` 0x8 p1 | 12 / 2 |
| `381d5387` | *STUConfigVarGetEntityCombinedHealth* | CVNumeric | 0x18 | +10 m_entity:CV* | `0x16200f8` 0x8 p1 | 12 / 11 |
| `61c58fac` |  | CVBoolBase | 0x10 |  | `0x15e29e0` 0x8 p2 | 12 / 4 |
| `adc6d1a0` |  | CVBoolBase | 0x10 |  | `0x15e24a0` 0x8 p2 | 12 / 8 |
| `221f6fb0` | *STUConfigVarShowMode* | CVBoolBase | 0x10 |  | `0x1606428` 0x8 p0 | 11 / 9 |
| `e4302ab4` | *STUConfigVarAbilityStatus* | CVEnum | 0x18 | +10 m_status:enum | `0x15dec90` 0x8 p2 | 9 / 5 |
| `7c39e5e6` | STUConfigVarGetMap | CVResource | 0x10 |  | `0x161c498` 0x8 p1 | 9 / 5 |
| `ee8d83b9` |  | CVEntityID | 0x10 |  | `0x15e3550` 0x8 p2 | 9 / 1 |
| `f97d9973` | *STUConfigVarGetRoundWinningTeam* | CVIntBase | 0x10 |  | `0x161e338` 0x8 p1 | 8 / 5 |
| `e0d68734` | *STUConfigVarStringTime* | CVStringBase | 0x18 | +10 f13ba264:CV* | `0x161c3b8` 0x8 p1 | 8 / 6 |
| `1a163481` |  | CVBoolBase | 0x18 | +10 m_type:enum | `0x15e06e0` 0x8 p2 | 8 / 3 |
| `3f836f1a` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x161fd78` 0x8 p1 | 8 / 4 |
| `d3eb1b97` |  | CVNumeric | 0x18 | +10 172f02ef:enum | `0x15e14e0` 0x8 p2 | 8 / 1 |
| `09e7eb88` | *STUConfigVarInCustomGame* | CVBoolBase | 0x10 |  | `0x15e0fa0` 0x8 p2 | 7 / 3 |
| `a9bd7ad3` | *STUConfigVarIsPartyLeader* | CVBoolBase | 0x10 |  | `0x15e1080` 0x8 p2 | 7 / 1 |
| `8d4869d1` | STUConfigVarGetEntityBoundsDimensions | CVVecBase | 0x18 | +10 m_entity:CV* | `0x15e46d0` 0x8 p2 | 6 / 2 |
| `74bcf7ab` | STUConfigVarGetEntityHero | CVResource | 0x18 | +10 m_entity:CV* | `0x161fbb8` 0x8 p1 | 6 / 3 |
| `31c142e1` | STUConfigVarGetEntityUpVector | CVVecBase | 0x18 | +10 m_entity:CV* | `0x16209b8` 0x8 p1 | 6 / 5 |
| `a08d3c3b` | STUConfigVarGetEntityVelocityVector | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1620a98` 0x8 p1 | 6 / 5 |
| `2860a6dc` | *STUConfigVarGetHeroLoadoutName* | CVGetHeroLoadoutBase | 0x20 |  | `0x15e0440` 0x8 p2 | 6 / 2 |
| `7705e5e6` | *STUConfigVarGetOvertimeRemaining* | CVNumeric | 0x10 |  | `0x161e418` 0x8 p1 | 6 / 3 |
| `cbd4f4f2` | STUConfigVarGetThrottle | CVVecBase | 0x18 | +10 m_entity:CV* | `0x161c2d8` 0x8 p1 | 6 / 4 |
| `4d35f2bd` | *STUConfigVarInMatchmaking* | CVBoolBase | 0x10 |  | `0x15e08a0` 0x8 p2 | 6 / 2 |
| `bd1b3274` | STUConfigVarMap | CVResource | 0x20 | +10 m_map:ref | `0x160aab8` 0x8 p0 | 6 / 3 |
| `81215cb9` |  | CVStringBase | 0x18 | +10 m_displayText:CV* | `0x15e2120` 0x8 p2 | 6 / 2 |
| `b53e9d8c` |  | CVBoolBase | 0x10 |  | `0x15e3b70` 0x8 p2 | 6 / 4 |
| `d48e6dfc` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15e0980` 0x8 p2; `0x16216d8` 0x8 p1 | 6 / 4 |
| `5529c204` | *STUConfigVarGetMapName* | CVResource | 0x18 | +10 m_map:CV* | `0x161c578` 0x8 p1 | 5 / 4 |
| `6774a48a` | *STUConfigVarIntRandom* | CVIntBase | 0x18 | +10 m_min:i32, +14 m_max:i32 | `0x16074c8` 0x8 p0 | 5 / 1 |
| `14b5511f` |  | CVResource | 0x18 | +10 3d18e189:CV* | `0x15e5230` 0x8 p2 | 5 / 5 |
| `1748b9b4` |  | CVNumeric | 0x10 |  | `0x15e53f0` 0x8 p2 | 5 / 2 |
| `26e39f4c` |  | CVVecBase | 0x20 | +10 0219eab2:CV*, +18 fa0d02b9:CV* | `0x161c818` 0x8 p1 | 5 / 3 |
| `289928c2` |  | CVVecBase | 0x30 | +10 e989044f:CV*, +18 2579faf0:CV*, +20 m_entity:CV*, +28 m_hardPoint:CV* | `0x161ce38` 0x8 p1 | 5 / 3 |
| `3dc364bd` |  | CVNumeric | 0x18 | +10 m_entity:CV* | `0x161f218` 0x8 p1 | 5 / 4 |
| `56a2012b` |  | CVBoolBase | 0x20 | +10 fb524c40:CV*, +18 m_radius:CV* | `0x15e3710` 0x8 p2 | 5 / 1 |
| `6f8414fe` |  | CVVecBase | 0x48 | +10 b0c994d6:CV*, +18 2c419df5:CV*, +20 f7e8d02a:CV*, +28 70e0c7ba:CV*, +30 m_collisionRadius:CV*, +38 142abd7e:CV*, … | `0x15e1b00` 0x8 p2 | 5 / 2 |
| `799d8dcd` |  | CVNumeric | 0x18 | +10 m_entity:CV* | `0x161f138` 0x8 p1 | 5 / 4 |
| `8c92c925` |  | CVResource | 0x18 | +10 m_hero:CV* | `0x15df9b0` 0x8 p2 | 5 / 2 |
| `a45d936b` | *STUConfigVarGetReticleRadius* | CVNumeric | 0x18 | +10 a7947530:CV* | `0x15e1320` 0x8 p2 | 4 / 4 |
| `1941ae68` |  | CVNumeric | 0x10 |  | `0x161ba18` 0x8 p1 | 4 / 1 |
| `5e7fbc32` |  | CVBoolBase | 0x20 | +10 m_entity:CV*, +18 61d0513d:CV* | `0x15e40b0` 0x8 p2 | 4 / 4 |
| `61404936` |  | CVEntityID | 0x10 |  | `0x15e4f90` 0x8 p2 | 4 / 1 |
| `9f762d9f` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15e2c80` 0x8 p2 | 4 / 2 |
| `518af43a` | STUConfigVarGetEntityBasePosition | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1620d38` 0x8 p1 | 3 / 2 |
| `36e0dbe6` | *STUConfigVarGetEntityViewTarget* | CVEntityID | 0x18 | +10 m_entity:CV* | `0x161eb18` 0x8 p1 | 3 / 1 |
| `bd455677` | STUConfigVarGetHeroName | CVStringBase | 0x18 | +10 m_hero:CV* | `0x15e4c10` 0x8 p2 | 3 / 2 |
| `b09ae0a8` | *STUConfigVarHeroColor* | CVColorBase | 0x20 | +10 m_hero:CV*, +18 32410b8f:CV* | `0x15debb0` 0x8 p2 | 3 / 1 |
| `0eef80e2` | *STUConfigVarIsModeReplay* | CVBoolBase | 0x10 |  | `0x15e30e0` 0x8 p2 | 3 / 3 |
| `9ffbfbb7` | *STUConfigVarIsPlatformWindows* | CVBoolBase | 0x10 |  | `0x15e1f60` 0x8 p2 | 3 / 2 |
| `6c916b7e` | *STUConfigVarLightingLayer* | CVResource | 0x20 | +10 0c7b4e74:ref | `0x1609bd8` 0x8 p0 | 3 / 3 |
| `3c640582` |  | CVBoolBase | 0x10 |  | `0x15e2d60` 0x8 p2 | 3 / 2 |
| `4b5d2f3f` |  | CVNumeric | 0x18 | +10 f26a2fc7:CV* | → STUConfigVar | 3 / 1 |
| `52520e3f` |  | CVNumeric | 0x20 | +10 m_entity:CV*, +18 m_hardPoint:CV* | `0x161f058` 0x8 p1 | 3 / 1 |
| `63c25b10` |  | CVResource | 0x10 |  | `0x161bd98` 0x8 p1 | 3 / 2 |
| `88dcbae4` |  | CVBoolBase | 0x10 |  | `0x15e1240` 0x8 p2 | 3 / 1 |
| `8fdc7ac0` |  | CVEntityID | 0x10 |  | `0x15e1da0` 0x8 p2 | 3 / 1 |
| `9db8e538` |  | CVBoolBase | 0x10 |  | `0x15e3c50` 0x8 p2 | 3 / 3 |
| `b6e8a8c3` |  | CVNumeric | 0x18 | +10 bbf5a51c:CV* | `0x15e01a0` 0x8 p2 | 3 / 1 |
| `bced5f1e` |  | CVNumeric | 0x20 | +10 549c2c51:CV*, +18 00324b8c:CV* | `0x161ef78` 0x8 p1 | 3 / 3 |
| `e2c530ff` |  | CVBoolBase | 0x10 |  | `0x15e2ac0` 0x8 p2 | 3 / 2 |
| `e4a99cc5` |  | CVBoolBase | 0x20 | +10 m_entity:CV*, +18 e5e4595a:CV* | `0x161cc78` 0x8 p1 | 3 / 1 |
| `aaa8450c` | STUConfigVarEntityReference | CVEntityID | 0x20 | +10 m_entityRef:809899f3 | `0x161d1b8` 0x8 p1 | 2 / 2 |
| `66665893` | STUConfigVarGetEntityCollisionRadius | CVEntityID | 0x18 | +10 m_entity:CV* | `0x1620558` 0x8 p1 | 2 / 2 |
| `6f53d515` | *STUConfigVarGetEntityIsSpectator* | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x161fc98` 0x8 p1 | 2 / 2 |
| `5852c346` | STUConfigVarGetMapTease | CVResource | 0x18 | +10 m_map:CV* | `0x161c658` 0x8 p1 | 2 / 1 |
| `dc0a8d4e` | *STUConfigVarGetMaxOvertimeLength* | CVNumeric | 0x10 |  | `0x161e4f8` 0x8 p1 | 2 / 2 |
| `38f63436` | *STUConfigVarGetNextMap* | CVResource | 0x10 |  | `0x15e5070` 0x8 p2 | 2 / 2 |
| `e32c898e` | *STUConfigVarGroundPitch* | CVNumeric | 0x28 | +10 m_entity:CV*, +18 m_offset:CV*, +20 m_height:CV* | `0x161f2f8` 0x8 p1 | 2 / 1 |
| `5a3b073f` | STUConfigVarHero | CVResource | 0x20 | +10 m_hero:ref | `0x1609a18` 0x8 p0 | 2 / 2 |
| `566f2c74` | *STUConfigVarIsHeroPlayable* | CVBoolBase | 0x18 | +10 m_hero:CV* | `0x15e0d00` 0x8 p2 | 2 / 1 |
| `82dd21b8` | *STUConfigVarStringTexture* | CVStringBase | 0x18 | +10 m_texture:CV* | `0x15e2040` 0x8 p2 | 2 / 1 |
| `00f37d1d` |  | CVIterable | 0x10 |  | `0x15e3460` 0x10 p2 | 2 / 1 |
| `27def0bb` |  | CVNumeric | 0x18 | +10 m_entity:CV* | `0x16201d8` 0x8 p1 | 2 / 2 |
| `393f74f3` |  | CVNumeric | 0x10 |  | `0x15e0ec0` 0x8 p2 | 2 / 1 |
| `43a1d25f` |  | CVBoolBase | 0x10 |  | `0x16065e8` 0x8 p0 | 2 / 2 |
| `4abdb7da` |  | CVBoolBase | 0x10 |  | `0x15df710` 0x8 p2 | 2 / 2 |
| `663394d9` |  | CVVecBase | 0x18 | +10 02fb5a83:CV* | `0x161d618` 0x8 p1 | 2 / 1 |
| `6f24eb76` |  | CVBoolBase | 0x10 |  | `0x15e3000` 0x8 p2 | 2 / 2 |
| `7c7e2ff8` |  | CVBoolBase | 0x18 | +10 61d0513d:CV* | `0x15e4270` 0x8 p2 | 2 / 2 |
| `7eaf2c13` |  | CVBoolBase | 0x10 |  | `0x15e07c0` 0x8 p2 | 2 / 2 |
| `9521c2b9` |  | CVNumeric | 0x20 | +10 a3fe58ce:CV*, +18 4d832c7b:CV* | `0x15e37f0` 0x8 p2 | 2 / 1 |
| `b1583185` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15e0280` 0x8 p2 | 2 / 1 |
| `b9af22db` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15e2820` 0x8 p2 | 2 / 2 |
| `bc3a5ec6` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15e0b40` 0x8 p2 | 2 / 1 |
| `d824c72b` |  | CVVecBase | 0x18 | +10 m_id:CV* | `0x15e0360` 0x8 p2 | 2 / 1 |
| `e7103546` |  | CVBoolBase | 0x28 | +10 m_entity:CV*, +18 m_raycast:CV*, +20 m_distance:CV* | `0x15e3e10` 0x8 p2 | 2 / 2 |
| `e81409c3` |  | CVVecBase | 0x30 | +10 m_inputs:CV*, +18 d4b3ae53:CV*, +20 dfdac7f0:CV*, +28 24dfb909:CV* | `0x15e1cc0` 0x8 p2 | 2 / 1 |
| `86503ec0` | STUConfigVarAnimation | CVAnimationBase | 0x20 | +10 m_animation:fileref | `0x160a738` 0x8 p0 | 1 / 1 |
| `f821d822` | *STUConfigVarGetCommandTargets* | CVIterable | 0x20 | +10 d5dde1fd:ref[] | `0x15e00b0` 0x10 p2 | 1 / 1 |
| `f0a4904d` | STUConfigVarGetEntityDefinition | CVEntityID | 0x18 | +10 m_entity:CV* | `0x161da78` 0x8 p1 | 1 / 1 |
| `897cb94a` | STUConfigVarGetEntitySlot | CVNumeric | 0x18 | +10 m_entity:CV* | `0x161fe58` 0x8 p1 | 1 / 1 |
| `e4d3b333` | *STUConfigVarGetEntitySurfaceVelocityVector* | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1620b78` 0x8 p1 | 1 / 1 |
| `babc78ad` | STUConfigVarGetFPS | CVString | 0x20 |  | `0x15dfb70` 0x8 p2 | 1 / 1 |
| `2b8efd68` | *STUConfigVarGetFireScore* | CVFloat | 0x18 |  | `0x15df0f0` 0x8 p2 | 1 / 1 |
| `86f201aa` | STUConfigVarGetHeroIcon | CVStringBase | 0x18 | +10 m_hero:CV* | `0x15e4430` 0x8 p2 | 1 / 1 |
| `e4be1517` | STUConfigVarGetHeroResponsible | CVEntityID | 0x18 | +10 m_entity:CV* | `0x161edb8` 0x8 p1 | 1 / 1 |
| `b40fa21b` | STUConfigVarGetHeroRole | CVResource | 0x18 | +10 m_hero:CV* | `0x15dfd30` 0x8 p2 | 1 / 1 |
| `18f4c87c` | *STUConfigVarGetLocalPlayerHero* | CVResource | 0x10 |  | `0x15e5150` 0x8 p2 | 1 / 1 |
| `25e74461` | *STUConfigVarGetLocalPlayerHeroLevel* | CVNumeric | 0x18 | +10 m_hero:CV* | `0x15e4dd0` 0x8 p2 | 1 / 1 |
| `16403206` | *STUConfigVarGetSelectedSpray* | CVResource | 0x18 | +10 m_entity:CV* | `0x15df7f0` 0x8 p2 | 1 / 1 |
| `d07ca4ab` | STUConfigVarGetStringLength | CVIntBase | 0x18 | +10 m_string:CV* | `0x15e22e0` 0x8 p2 | 1 / 1 |
| `5c73d1df` | *STUConfigVarIsInParty* | CVBoolBase | 0x10 |  | `0x15e1160` 0x8 p2 | 1 / 1 |
| `47173b0d` | *STUConfigVarIsPlatformMac* | CVBoolBase | 0x10 |  | `0x15e15c0` 0x8 p2 | 1 / 1 |
| `ae45e451` | *STUConfigVarIsPointInArea* | CVBoolBase | 0x20 | +10 fb524c40:CV*, +18 m_entity:CV* | `0x161d378` 0x8 p1 | 1 / 1 |
| `d0677667` | *STUConfigVarModelLOD* | CVIntBase | 0x18 | +10 0658fdd0:enum | `0x161d7d8` 0x8 p1 | 1 / 1 |
| `38901d1b` | STUConfigVarModelLook | CVResource | 0x20 | +10 m_modelLook:ref | `0x1609778` 0x8 p0 | 1 / 1 |
| `bd2cbd1f` | *STUConfigVarUXGetInterpolationDelay* | CVString | 0x20 |  | `0x15df630` 0x8 p2 | 1 / 1 |
| `3661ea61` | *STUConfigVarUXGetPing* | CVString | 0x20 |  | `0x15df390` 0x8 p2 | 1 / 1 |
| `6c40a6e3` | *STUConfigVarUXGetRTT* | CVString | 0x20 |  | `0x15df550` 0x8 p2 | 1 / 1 |
| `8d591677` | *STUConfigVarVec3Comp* | CVVecBase | 0x28 | +10 m_valueX:CV*, +18 m_valueY:CV*, +20 m_valueZ:CV* | `0x1606ea8` 0x8 p0 | 1 / 1 |
| `f36d34bb` | STUConfigVoiceDefinition | CVResource | 0x20 | +10 m_voiceDef:ref | `0x16095b8` 0x8 p0 | 1 / 1 |
| `01e1f3cf` |  | CVBoolBase | 0x10 |  | `0x15e1780` 0x8 p2 | 1 / 1 |
| `179167ef` |  | CVNumeric | 0x10 |  | `0x161b778` 0x8 p1 | 1 / 1 |
| `2c1c1e0d` |  | CVNumeric | 0x10 |  | `0x15e0c20` 0x8 p2 | 1 / 1 |
| `31993d91` |  | CVNumeric | 0x18 | +10 m_entity:CV* | → STUConfigVar | 1 / 1 |
| `3b52e9e4` |  | CVVecBase | 0x30 | +10 a8a3e0f7:CV*, +18 m_pivotPos:CV*, +20 955d10cd:CV*, +28 365e1278:enum | `0x161cd58` 0x8 p1 | 1 / 1 |
| `3d1af60d` |  | CVNumeric | 0x18 | +10 m_entity:CV* | `0x161fad8` 0x8 p1 | 1 / 1 |
| `3fd35d73` |  | CVNumeric | 0x10 |  | `0x15e39b0` 0x8 p2 | 1 / 1 |
| `407abbba` |  | CVResource | 0x18 | +10 m_map:CV* | `0x161c738` 0x8 p1 | 1 / 1 |
| `44602f47` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x161c1f8` 0x8 p1 | 1 / 1 |
| `4b0fa643` |  | CVBoolBase | 0x10 |  | `0x15e16a0` 0x8 p2 | 1 / 1 |
| `52cd7ccf` |  | CVNumeric | 0x10 |  | `0x15e1940` 0x8 p2 | 1 / 1 |
| `54ce444c` |  | CVBoolBase | 0x10 |  | `0x15e2660` 0x8 p2 | 1 / 1 |
| `5652dfaf` |  | CVBoolBase | 0x30 | +10 566e9b3e:CV*, +18 2c70ef65:CV*, +20 dd49b6b3:CV*, +28 6b6801bd:CV* | `0x161d0d8` 0x8 p1 | 1 / 1 |
| `5751daf0` |  | CVBoolBase | 0x10 |  | `0x15e4970` 0x8 p2 | 1 / 1 |
| `5ae61fbb` |  | CVResource | 0x20 | +10 m_map:CV*, +18 m_team:CV* | `0x161bf58` 0x8 p1 | 1 / 1 |
| `5d93b951` |  | CVBoolBase | 0x18 | +10 5e99eff1:CV* | `0x15e1400` 0x8 p2 | 1 / 1 |
| `6120e716` |  | CVResource | 0x18 | +10 m_hero:CV* | `0x15dfa90` 0x8 p2 | 1 / 1 |
| `740935b9` |  | CVBoolBase | 0x10 |  | `0x15e4890` 0x8 p2 | 1 / 1 |
| `7a4d3a04` |  | CVBoolBase | 0x10 |  | `0x1606508` 0x8 p0 | 1 / 1 |
| `7c912fd9` |  | CVIntBase | 0x10 |  | `0x15e1e80` 0x8 p2 | 1 / 1 |
| `7e3f566a` |  | CVResource | 0x18 | +10 m_entity:CV* | `0x161cab8` 0x8 p1 | 1 / 1 |
| `7e8dcef9` |  | CVBoolBase | 0x10 |  | `0x15dee50` 0x8 p2 | 1 / 1 |
| `8ab9197a` |  | CVNumeric | 0x10 |  | `0x15e3a90` 0x8 p2 | 1 / 1 |
| `a44338c8` |  | CVString | 0x20 |  | `0x15df470` 0x8 p2 | 1 / 1 |
| `aa995333` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15e3fd0` 0x8 p2 | 1 / 1 |
| `ad0d394e` |  | CVNumeric | 0x10 |  | `0x15e38d0` 0x8 p2 | 1 / 1 |
| `b4dd308c` |  | CVStringBase | 0x28 | +10 m_strings:CV*[], +20 0f5f7a8a:CV* | `0x161bbd8` 0x8 p1 | 1 / 1 |
| `c1694221` |  | CVEntityDefinitionBase | 0x20 | +10 m_entityDef:ref | `0x160a118` 0x8 p0 | 1 / 1 |
| `d1211491` |  | CVBoolBase | 0x18 | +10 dd6f5cf2:CV* | `0x15e4b30` 0x8 p2 | 1 / 1 |
| `e1524276` |  | CVBoolBase | 0x10 |  | `0x15e0a60` 0x8 p2 | 1 / 1 |
| `e82d42de` |  | CVNumeric | 0x18 | +10 m_entity:CV* | `0x161f4b8` 0x8 p1 | 1 / 1 |
| `e9fa3240` |  | CVResource | 0x20 | +10 m_map:CV*, +18 m_team:CV* | `0x161be78` 0x8 p1 | 1 / 1 |
| `ef277c2f` |  | CVVecBase | 0x30 | +10 120ddd57:CV*, +18 556d3f86:CV*, +20 5898cae1:CV*, +28 ba5686ad:CV* | `0x161cff8` 0x8 p1 | 1 / 1 |
| `f3f92e66` |  | CVBoolBase | 0x10 |  | `0x15df2b0` 0x8 p2 | 1 / 1 |
| `f5f38b1b` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x15def30` 0x8 p2 | 1 / 1 |
| `f7438661` |  | CVNumeric | 0x18 | +10 m_entity:CV* | `0x161f9f8` 0x8 p1 | 1 / 1 |
| `fcf6f7fd` |  | CVNumeric | 0x10 |  | `0x15e1a20` 0x8 p2 | 1 / 1 |
| `fedf6743` |  | CVBoolBase | 0x10 |  | `0x161b938` 0x8 p1 | 1 / 1 |
| `bfd9aadd` | STUConfigVar |  | 0x10 | +8 83e83924:u64 | `0x15de9e0` 0x8 p0 | – |
| `8d19741a` | *STUConfigVarAIDefinition* | CVResource | 0x20 | +10 9b2c967e:ref | `0x16093f8` 0x8 p0 | – |
| `266ca3bc` | STUConfigVarAnimationBase | CVResource | 0x10 |  | → STUConfigVar | – |
| `f77dddf0` | *STUConfigVarArrayEquals* | CVBoolBase | 0x20 | +10 f780c67c:CV*, +18 6e8997c6:CV* | `0x1607308` 0x8 p0 | – |
| `17e77fd2` | STUConfigVarBoolBase | CV | 0x10 |  | → STUConfigVar | – |
| `436c434b` | *STUConfigVarBoolRandom* | CVBoolBase | 0x18 | +10 6ea869da:f32 | `0x1607148` 0x8 p0 | – |
| `5678a808` | STUConfigVarColorBase | CV | 0x10 |  | → STUConfigVar | – |
| `2b46bc10` | *STUConfigVarDBID* | CV | 0x10 |  | → STUConfigVar | – |
| `4c35245d` | *STUConfigVarEntityDefinitionBase* | CVResource | 0x10 |  | → STUConfigVar | – |
| `bac93091` | STUConfigVarEntityID | CV | 0x10 |  | → STUConfigVar | – |
| `054bc7e8` | *STUConfigVarEnum* | CV | 0x10 |  | → STUConfigVar | – |
| `16c264e9` | STUConfigVarFloatBase | CVNumeric | 0x10 |  | → STUConfigVar | – |
| `2f48315d` | STUConfigVarGetAIDestination | CVVecBase | 0x18 | +10 m_entity:CV* | → STUConfigVar | – |
| `e7ea9321` | *STUConfigVarGetColorCodeString* | CVStringBase | 0x18 | +10 m_color:CV* | `0x15e1be0` 0x8 p2 | – |
| `d7aa244a` | STUConfigVarGetEntityBoundsCenter | CVVecBase | 0x18 | +10 m_entity:CV* | `0x15e45f0` 0x8 p2 | – |
| `a07b181c` | *STUConfigVarGetEntityProjectedPosition* | CVVecBase | 0x40 | +10 m_entity:CV*, +18 89f6c2a7:CV*, +20 m_initialVelocity:CV*, +28 m_isCrouching:CV*, +30 bd02981e:CV*, +38 b8458be9:CV* | `0x16210b8` 0x8 p1 | – |
| `732623c4` | STUConfigVarGetEscortPos | CVVecBase | 0x20 | +10 aba25697:CV*, +18 8ebeb779:CV* | → STUConfigVar | – |
| `3d2488d2` | STUConfigVarGetGravity | CVVecBase | 0x18 | +10 m_entity:CV* | `0x1621198` 0x8 p1 | – |
| `b6f726bb` | *STUConfigVarGetHeroLoadoutBase* | CVResource | 0x20 | +10 m_logicalButton:CV*, +18 m_entity:CV* | `0x15e0520` 0x8 p2 | – |
| `153a4773` | STUConfigVarGetHeroPreview | CVResource | 0x18 | +10 m_hero:CV* | `0x15e4350` 0x8 p2 | – |
| `b90a34a2` | *STUConfigVarGetIndexInArray* | CVIntBase | 0x20 | +10 81b41f3f:CV*, +18 edfb51d3:CVDynamic* | `0x1606348` 0x8 p0 | – |
| `9d90350e` | *STUConfigVarGetMapObjectiveIcon* | CVResource | 0x20 | +10 m_map:CV*, +18 m_team:CV* | `0x161c038` 0x8 p1 | – |
| `7a1040f7` | *STUConfigVarGetPlayerResponsible* | CVEntityID | 0x18 | +10 m_entity:CV* | `0x161ee98` 0x8 p1 | – |
| `38671ef6` | *STUConfigVarGetSelectedSprayEffect* | CVResource | 0x18 | +10 m_entity:CV* | `0x15dffd0` 0x8 p2 | – |
| `98dadbbf` | STUConfigVarGetSkinName | CVStringBase | 0x18 | +10 m_skin:CV* | `0x15e4510` 0x8 p2 | – |
| `aeef26c4` | *STUConfigVarGetSurfaceEntity* | CVEntityID | 0x18 | +10 m_entity:CV* | `0x1621518` 0x8 p1 | – |
| `90e9e721` | STUConfigVarHandle | CV | 0x10 |  | → STUConfigVar | – |
| `53b7d44e` | *STUConfigVarHasEscortPos* | CVBoolBase | 0x20 | +10 aba25697:CV*, +18 8ebeb779:CV* | → STUConfigVar | – |
| `ef79ccf9` | STUConfigVarIntBase | CVNumeric | 0x10 |  | → STUConfigVar | – |
| `1942ab16` | *STUConfigVarIsModeLobby* | CVBoolBase | 0x10 |  | `0x15e2e40` 0x8 p2 | – |
| `8cb6be1c` | STUConfigVarIterable | CV | 0x10 |  | → STUConfigVar | – |
| `f4c68fa2` | STUConfigVarNumeric | CV | 0x10 |  | → STUConfigVar | – |
| `65f4c9d9` | STUConfigVarResource | CV | 0x10 |  | → STUConfigVar | – |
| `098c01a7` | STUConfigVarSkin | CVSkinBase | 0x20 | +10 m_skin:ref | `0x1609af8` 0x8 p0 | – |
| `ff20da09` | STUConfigVarSkinBase | CVResource | 0x10 |  | → STUConfigVar | – |
| `d4239a46` | STUConfigVarSkinTheme | CVSkinBase | 0x20 | +10 m_skinTheme:ref | → STUConfigVar | – |
| `4ff6e110` | STUConfigVarStringBase | CV | 0x10 |  | → STUConfigVar | – |
| `0d1c206a` | *STUConfigVarUXEnum* | CVResource | 0x20 | +10 m_value:str | → STUConfigVar | – |
| `c6e89b8f` | STUConfigVarVecBase | CV | 0x10 |  | → STUConfigVar | – |
| `cd35ad96` | STUConfigVarVoiceStimulus | CVResource | 0x20 | +10 m_voiceStimulus:ref | `0x1609698` 0x8 p0 | – |
| `001e61e7` |  | CVBoolBase | 0x20 | +10 m_target:CV*, +18 c4e6bd4e:CV* | `0x161e958` 0x8 p1 | – |
| `0212b263` |  | CVBoolBase | 0x10 |  | `0x15e4a50` 0x8 p2 | – |
| `0fe5d384` |  | CVVecBase | 0x20 | +10 c4e6bd4e:CV*, +18 ba35b799:CV* | → STUConfigVar | – |
| `10770942` |  | CVNumeric | 0x20 | +10 m_entity:CV*, +18 m_hardPoint:CV* | `0x161f838` 0x8 p1 | – |
| `11d0491f` |  | CVVecBase | 0x18 | +10 m_entity:CV* | `0x15e3d30` 0x8 p2 | – |
| `151f6b00` |  | 63646258 | 0x10 |  | → STUConfigVar | – |
| `170c31a9` |  | CVBoolBase | 0x10 |  | `0x15e1860` 0x8 p2 | – |
| `19747a9a` |  | CVNumeric | 0x18 | +10 m_entity:CV* | → STUConfigVar | – |
| `1a219ed5` |  | CVIntBase | 0x18 | +10 712bdaf2:enum | `0x161dd18` 0x8 p1 | – |
| `22316e6f` |  | CVBoolBase | 0x28 | +10 bd052c19:CV*, +18 m_skin:CV*, +20 3cb5f47d:u8 | `0x161dfb8` 0x8 p1 | – |
| `29969708` |  | CVResource | 0x10 |  | `0x15df010` 0x8 p2 | – |
| `2f7798ab` |  | CVVecBase | 0x18 | +10 e8c59c38:CV* | → STUConfigVar | – |
| `409c207f` |  | CVIntBase | 0x18 | +10 m_entity:CV* | → STUConfigVar | – |
| `45a80dd1` |  | CVVecBase | 0x20 | +10 93c1a2ad:CV*, +18 0089edcf:CV* | `0x161c8f8` 0x8 p1 | – |
| `471067bb` |  | CVBoolBase | 0x10 |  | `0x15ded70` 0x8 p2 | – |
| `4c8c39c1` |  | CVBoolBase | 0x28 | +10 m_target:CV*, +18 fbdafef2:CV*, +20 m_forWeapon:CV* | `0x161e178` 0x8 p1 | – |
| `4dc04d19` |  | CVEntityID | 0x10 |  | → STUConfigVar | – |
| `50d1611e` |  | CVNumeric | 0x30 | +10 m_target:CV*, +18 cab08616:CV*, +20 ce4b994f:CV*, +28 f1ad8cc3:CV* | → STUConfigVar | – |
| `50f7f0dd` |  | CVBoolBase | 0x10 |  | `0x15e32a0` 0x8 p2 | – |
| `51f04ae8` |  | CVNumeric | 0x20 | +10 m_target:CV*, +18 c4e6bd4e:CV* | `0x161e098` 0x8 p1 | – |
| `63646258` |  | CV | 0x10 |  | → STUConfigVar | – |
| `6aba9763` |  | CVNumeric | 0x28 | +10 d64804e1:CV*, +18 m_defaultValue:CV*, +20 6c68cb17:CV* | → STUConfigVar | – |
| `6b69cdef` |  | CVNumeric | 0x20 | +10 m_target:CV*, +18 c4e6bd4e:CV* | `0x161ea38` 0x8 p1 | – |
| `721c3966` |  | CVIntBase | 0x18 | +10 f8b6c20e:enum | `0x161ded8` 0x8 p1 | – |
| `7415827c` |  | CVResource | 0x18 | +10 m_entity:CV* | `0x16208d8` 0x8 p1 | – |
| `78c3f68d` |  | CV | 0x10 |  | → STUConfigVar | – |
| `7c0bc2b9` |  | CVVecBase | 0x18 | +10 m_entity:CV* | → STUConfigVar | – |
| `80c17bc1` |  | CVIntBase | 0x18 | +10 712bdaf2:enum | `0x161ddf8` 0x8 p1 | – |
| `8a8fc811` |  | CVIntBase | 0x18 | +10 78ae1bab:enum | `0x161dc38` 0x8 p1 | – |
| `8d17a92a` |  | CVIntBase | 0x18 | +10 c86e11c2:enum | → STUConfigVar | – |
| `8ff697dd` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | → STUConfigVar | – |
| `9960eba6` |  | CVNumeric | 0x18 | +10 59b47edb:CV* | → STUConfigVar | – |
| `a4a3e12c` |  | CVBoolBase | 0x18 | +10 e8c59c38:CV* | → STUConfigVar | – |
| `a661d814` |  | CVBoolBase | 0x20 | +10 bd052c19:CV*, +18 m_skin:CV* | `0x161d6f8` 0x8 p1 | – |
| `ae8e9502` |  | CVResource | 0x18 | +10 m_hero:CV* | `0x15df8d0` 0x8 p2 | – |
| `b425d325` |  | CVVecBase | 0x10 |  | `0x15e3380` 0x8 p2 | – |
| `bf58cf0c` |  | CVVecBase | 0x20 | +10 a8a3e0f7:CV*, +18 m_entity:CV* | `0x161cf18` 0x8 p1 | – |
| `c5384890` |  | CVEntityID | 0x18 | +10 m_entity:CV* | → STUConfigVar | – |
| `dcdd09dc` |  | CVNumeric | 0x20 | +10 m_entity:CV*, +18 m_hardPoint:CV* | `0x161f918` 0x8 p1 | – |
| `e1254f43` |  | CVVecBase | 0x20 | +10 m_posA:CV*, +18 m_posB:CV* | `0x161b858` 0x8 p1 | – |
| `e4fc31f9` |  | CVEntityID | 0x18 | +10 m_entity:CV* | → STUConfigVar | – |
| `f6340f00` |  | CVEntityID | 0x20 | +10 fbdafef2:CV*, +18 1d953a5e:CV* | `0x161e6b8` 0x8 p1 | – |
| `f9e400ec` |  | CVNumeric | 0x28 | +10 m_target:CV*, +18 cab08616:CV*, +20 f1ad8cc3:CV* | → STUConfigVar | – |
| `fbfd185d` |  | CVNumeric | 0x28 | +10 fbdafef2:CV*, +18 6c8fd5a1:CV*, +20 1d953a5e:CV* | `0x161e798` 0x8 p1 | – |
| `fca32e70` |  | CVBoolBase | 0x18 | +10 m_entity:CV* | `0x161e258` 0x8 p1 | – |

## Other STUStatescriptBase subclasses (2)

| hash | name | parent | STU | own fields | runtime | uses |
|---|---|---|---|---|---|---|
| `138ccab0` | *STUStatescriptAndGate* | SSBase | 0xa0 | +78 c77035be:i32[], +88 m_inPlug:SSInputPlug*, +90 m_outPlug:SSOutputPlug*, +98 5e756553:i32 | `0x1604bd8` 0x8 p0 | 5 / 4 |
| `a97578e7` | STUStatescriptBase | STUGraphNode | 0x78 | +38 m_debugText:str, +48 f8f89f89:SSRemoteSyncVar[], +58 a56dc772:SSRemoteSyncVar[], +68 53bfb779:i32, +6c 4b062861:i32, +70 efd56e99:enum, +74 m_isState:u8, +75 m_clientOnly:u8, +76 m_serverOnly:u8 | – | – |
