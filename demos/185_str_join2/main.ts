function has(w: string): i32 {
  if (w == "b") {
    return 1;
  }
  return 0;
}
function main(): i32 {
  const words: string[] = ["a", "b", "c"];
  let n: i32 = 0;
  for (const w of words) {
    n = n + has(w);
  }
  console.log(words.length, n);
  return 0;
}
