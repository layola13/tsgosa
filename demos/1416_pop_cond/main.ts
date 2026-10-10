function main(): i32 {
  const a: i32[] = [5];
  let s: i32 = 0;
  while (a.length > 0) {
    s = s + a.pop();
  }
  console.log(s);
  return 0;
}
