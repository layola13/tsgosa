class C {
  v: i32 = 0;
}
function main(): i32 {
  const a = [10, 20];
  console.log((a).length);
  const m = new Map<string, i32>();
  m.set("k", 5);
  console.log((m).get("k"));
  const c = new (C)();
  c.v = 3;
  console.log((c).v);
  return 0;
}
