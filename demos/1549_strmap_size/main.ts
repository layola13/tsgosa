function main(): i32 {
  const m = new Map<string, string>();
  m.set("a", "x"); m.set("b", "y");
  console.log(m.size);
  console.log(m.has("a") ? 1 : 0);
  return 0;
}
