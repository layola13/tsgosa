function main(): i32 {
  const s: string = "hey";
  let n = 0;
  for (const c of s) { n += 1; }
  console.log(n);
  return 0;
}
