function main(): i32 {
  const m = new Map<string, number>();
  m.set("a", 1);
  const f: boolean = true;
  if (f == m.get("a")) { console.log(1); } else { console.log(0); }
  const x: boolean = m.get("zz") == f;
  console.log(x);
  return 0;
}
