function main(): i32 {
  const m = new Map<string, number>();
  m.set("a", 1);
  const f: boolean = true;
  const t: boolean = false;
  console.log(f == m.get("a"));
  console.log(f == m.get("zz"));
  console.log(t == m.get("a"));
  console.log(t != m.get("zz"));
  return 0;
}
