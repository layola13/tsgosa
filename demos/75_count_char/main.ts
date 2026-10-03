function main(): i32 {
  const s: string = "hello";
  let n: i32 = 0;
  for (let i: i32 = 0; i < s.length; i++) {
    if (s.charAt(i) == "l") {
      n = n + 1;
    }
  }
  console.log(n);
  return 0;
}
