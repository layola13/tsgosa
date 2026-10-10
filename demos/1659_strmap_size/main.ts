function main(): i32 {
  const m = new Map<string, string>();
  m.set("lang", "ts");
  console.log(m.get("lang") ?? "js");
  console.log(m.size);
  return 0;
}
