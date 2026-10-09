function pick(a: string | null, b: string): string {
  return a ?? b;
}
console.log(pick(null, "z"));
console.log(pick("", "z").length);
