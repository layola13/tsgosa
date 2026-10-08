function tag(s: string[], v: i32): i32 {
  console.log(s.length);
  return v + 1;
}
function main(): i32 {
  console.log(tag`hi${41}`);
  return 0;
}
