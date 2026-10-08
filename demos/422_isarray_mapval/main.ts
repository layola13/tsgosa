function main(): i32 {
  const m = new Map<string, string[]>();
  m.set("k", ["a"]);
  const v = m.get("k");
  if (v !== undefined) {
    console.log(Array.isArray(v) ? 1 : 0);
  }
  return 0;
}
