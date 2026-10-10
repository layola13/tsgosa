function main(): i32 {
  const s: string = "hi";
  console.log(s.padEnd(5, "!"));
  console.log(s.padStart(4, "*"));
  return 0;
}
