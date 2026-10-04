function isVowel(c: string): i32 {
  return c == "a" || c == "e" || c == "i" || c == "o" || c == "u";
}
function main(): i32 {
  const s: string = "hello";
  let n: i32 = 0;
  for (let i: i32 = 0; i < s.length; i++) {
    n = n + isVowel(s.charAt(i));
  }
  console.log(n);
  return 0;
}
