function main(): i32 {
  const m = new Map<string, i32>();
  m.set("p", 7);
  m.set("q", 8);
  console.log(m.get("p") ?? -1);
  console.log(m.get("q") ?? -1);
  return 0;
}
