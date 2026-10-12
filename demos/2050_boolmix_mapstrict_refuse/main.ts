function main(): i32 {
  const m = new Map<string, number>();
  m.set("a", 1);
  const f: boolean = true;
  console.log(f === m.get("a"));
  return 0;
}
