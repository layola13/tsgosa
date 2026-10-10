function main(): i32 {
  const s: string = "hello";
  const i = 1;
  console.log(s[i]);
  console.log(s.charAt(i + 1));
  console.log(s.slice(i, i + 3));
  return 0;
}
