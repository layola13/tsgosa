function shout(parts: string[]): string {
  return parts[0];
}
function main(): i32 {
  console.log(shout`hi`);
  return 0;
}
