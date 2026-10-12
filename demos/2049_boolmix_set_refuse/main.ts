function main(): i32 {
  const s = new Set<string>();
  s.add("a");
  const f: boolean = true;
  console.log(f == s.has("a"));
  return 0;
}
