export function main(): i32 {
  const chars = "abc".split("");
  console.log(chars.length);
  console.log(chars[0]);
  console.log(chars[2]);
  const parts = "a,b".split(",");
  console.log(parts.length);
  console.log(parts[1]);
  return 0;
}
