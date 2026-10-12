function main(): i32 {
  const m = new Map<string, string>();
  m.set("a", "x");
  const f: boolean = true;
  console.log(f == m.get("a"));
  return 0;
}
