function shout(parts: string[]): string {
  return parts[0];
}
function main(): i32 {
  const s: string = shout`yo`;
  console.log(s);
  return 0;
}
