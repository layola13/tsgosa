function main(): i32 {
  const s: string = "hello";
  let n = 0;
  for (let i = 0; i < s.length; i = i + 1) { if (s[i] == "l") { n = n + 1; } }
  console.log(n);
  return 0;
}
