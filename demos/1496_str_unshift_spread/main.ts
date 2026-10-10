function main(): i32 {
  const s: string[] = ["c"];
  const t: string[] = ["a", "b"];
  s.unshift(...t);
  console.log(s.length);
  console.log(s[0]);
  console.log(s[1]);
  return 0;
}
