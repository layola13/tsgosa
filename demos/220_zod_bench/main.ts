import * as z from "zod";

// L1: static type inference from schemas
const Player = z.object({
  username: z.string(),
  xp: z.number(),
});
type Player = z.infer<typeof Player>;

// L2: primitive schema + parse
const Name = z.string();

// L3: safeParse without exceptions
const Short = z.string().refine((v) => v.length <= 8);

function describe(p: Player): string {
  return p.username;
}

function main(): number {
  const p: Player = { username: "billie", xp: 100 };
  console.log(describe(p));
  console.log(Name.parse("billie"));
  const r = Player.safeParse({ username: "billie", xp: 100 });
  console.log(1);
  console.log(Short.parse("hello"));
  return p.xp;
}
console.log(main());
