function get(): string|null { return "hi"; }
function main(): i32 {
  const s = get()!;
  console.log(s.length);
  return 0;
}
