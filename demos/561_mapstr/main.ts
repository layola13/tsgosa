function main(): i32 {
  const m = new Map<string, string>();
  m.set("k", "v");
  console.log(m.get("k"));
  return 0;
}
