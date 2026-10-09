function pick(x: string | null): string | null {
  return x;
}
console.log(pick(null) ?? "d");
console.log(pick("a") ?? "d");
