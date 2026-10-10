function main(): i32 {
  const s: string = "hello";
  console.log(s.startsWith("he") ? 1 : 0);
  console.log(s.endsWith("lo") ? 1 : 0);
  console.log(s.includes("ll") ? 1 : 0);
  return 0;
}
