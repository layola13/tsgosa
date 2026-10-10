function main(): i32 {
  const s: string[] = ["a"];
  const t: string[] = ["b", "c"];
  s.push(...t);
  console.log(s.length);
  console.log(s[2]);
  return 0;
}
